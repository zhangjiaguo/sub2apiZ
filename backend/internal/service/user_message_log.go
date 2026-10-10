// Package service —— 用户进站消息明文留存（独立于提示词审计：纯记录）。
// 提交侧只做入队（满即丢，绝不阻塞请求）；协议文本抽取由 handler 层的
// thunk 在后台完成，本服务负责缓冲落库与保留期清理。
// 保留期默认 7 天，环境变量 SUB2API_USER_MESSAGE_RETENTION_DAYS 可覆盖。
package service

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	UserMessageQueueCapacity = 8192
	userMessageFlushInterval = 500 * time.Millisecond
	userMessageFlushBatch    = 128
)

// UserMessageLog 是一条进站消息的明文留存记录。
type UserMessageLog struct {
	ID            int64     `json:"id"`
	CreatedAt     time.Time `json:"created_at"`
	RequestID     string    `json:"request_id"`
	UserID        int64     `json:"user_id"`
	Username      string    `json:"username"`
	UserEmail     string    `json:"user_email"`
	APIKeyID      int64     `json:"api_key_id"`
	APIKeyName    string    `json:"api_key_name"`
	GroupID       int64     `json:"group_id"`
	GroupName     string    `json:"group_name"`
	Provider      string    `json:"provider"`
	Endpoint      string    `json:"endpoint"`
	Protocol      string    `json:"protocol"`
	Model         string    `json:"model"`
	Stream        bool      `json:"stream"`
	Stage         string    `json:"stage"`
	Content       string    `json:"content"`
	ContentLength int       `json:"content_length"`
	Truncated     bool      `json:"truncated"`
}

// UserMessageLogFilter 是列表查询条件。
type UserMessageLogFilter struct {
	Page      int
	PageSize  int
	UserID    *int64
	GroupID   *int64
	Model     string
	Query     string
	StartTime *time.Time
	EndTime   *time.Time
	// AllowedGroupIDs 由查看者（非管理员）路径注入：跨多授权分组聚合查询。
	AllowedGroupIDs []int64
}

// UserMessageLogList 是分页结果（列表项 Content 为前 300 字符预览）。
type UserMessageLogList struct {
	Items    []*UserMessageLog `json:"items"`
	Total    int64             `json:"total"`
	Page     int               `json:"page"`
	PageSize int               `json:"page_size"`
}

// ErrUserMessageLogNotFound 查询单条不存在。
var ErrUserMessageLogNotFound = errors.New("user message log not found")

// UserMessageLogRepository 由 repository 层实现。
type UserMessageLogRepository interface {
	InsertBatch(ctx context.Context, logs []*UserMessageLog) error
	List(ctx context.Context, filter *UserMessageLogFilter) (*UserMessageLogList, error)
	Get(ctx context.Context, id int64) (*UserMessageLog, error)
	CleanupBefore(ctx context.Context, cutoff time.Time) (int64, error)
}

// userMessageRetentionDays 是消息留存保留期（天）。
func userMessageRetentionDays() int {
	value := strings.TrimSpace(os.Getenv("SUB2API_USER_MESSAGE_RETENTION_DAYS"))
	if value != "" {
		if days, err := strconv.Atoi(value); err == nil && days >= 1 && days <= 3650 {
			return days
		}
	}
	return 7
}

type UserMessageLogService struct {
	repo       UserMessageLogRepository
	viewerRepo UserMessageViewerRepository
	queue      chan func() *UserMessageLog

	mu     sync.Mutex
	cancel context.CancelFunc
	wg     sync.WaitGroup

	dropped int64 // 队满丢弃计数（仅观测）
}

func ProvideUserMessageLogService(repo UserMessageLogRepository, viewerRepo UserMessageViewerRepository) *UserMessageLogService {
	return &UserMessageLogService{
		repo:       repo,
		viewerRepo: viewerRepo,
		queue:      make(chan func() *UserMessageLog, UserMessageQueueCapacity),
	}
}

// Submit 入队一条消息（extract 在后台执行，返回 nil 表示跳过）。满即丢。
func (s *UserMessageLogService) Submit(extract func() *UserMessageLog) {
	if s == nil || extract == nil {
		return
	}
	select {
	case s.queue <- extract:
	default:
		s.mu.Lock()
		s.dropped++
		dropped := s.dropped
		s.mu.Unlock()
		if dropped%1000 == 1 {
			slog.Warn("user_message_log_queue_full", "dropped_total", dropped)
		}
	}
}

// Start 启动后台落库与保留期清理。
func (s *UserMessageLogService) Start(ctx context.Context) error {
	if s == nil || s.repo == nil {
		return nil
	}
	s.mu.Lock()
	if s.cancel != nil {
		s.mu.Unlock()
		return nil
	}
	background, cancel := context.WithCancel(ctx)
	s.cancel = cancel
	s.mu.Unlock()

	s.wg.Add(1)
	go s.flushLoop(background)
	s.wg.Add(1)
	go s.retentionLoop(background)
	return nil
}

// Stop 停止后台任务并尽力冲刷缓冲。
func (s *UserMessageLogService) Stop(ctx context.Context) error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	cancel := s.cancel
	s.cancel = nil
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	done := make(chan struct{})
	go func() { s.wg.Wait(); close(done) }()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *UserMessageLogService) flushLoop(ctx context.Context) {
	defer s.wg.Done()
	ticker := time.NewTicker(userMessageFlushInterval)
	defer ticker.Stop()
	batch := make([]*UserMessageLog, 0, userMessageFlushBatch)
	flush := func() {
		if len(batch) == 0 {
			return
		}
		if err := s.repo.InsertBatch(ctx, batch); err != nil {
			// 落库失败丢弃本批：消息留存有保留期上限，不值得重试放大内存。
			slog.Warn("user_message_log_insert_failed", "rows", len(batch), "error", err)
		}
		batch = batch[:0]
	}
	for {
		select {
		case <-ctx.Done():
			// 停机时尽力排空已入队的解析任务。
			for {
				select {
				case extract := <-s.queue:
					if entry := extract(); entry != nil {
						batch = append(batch, entry)
						if len(batch) >= userMessageFlushBatch {
							flush()
						}
					}
					continue
				default:
				}
				break
			}
			flush()
			return
		case extract := <-s.queue:
			if entry := extract(); entry != nil {
				batch = append(batch, entry)
				if len(batch) >= userMessageFlushBatch {
					flush()
				}
			}
		case <-ticker.C:
			flush()
		}
	}
}

func (s *UserMessageLogService) retentionLoop(ctx context.Context) {
	defer s.wg.Done()
	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.cleanupOnce(ctx)
		}
	}
}

func (s *UserMessageLogService) cleanupOnce(ctx context.Context) {
	days := userMessageRetentionDays()
	deleted, err := s.repo.CleanupBefore(ctx, time.Now().Add(-time.Duration(days)*24*time.Hour))
	if err != nil {
		slog.Warn("user_message_log_cleanup_failed", "error", err)
		return
	}
	if deleted > 0 {
		slog.Info("user_message_log_retention_cleaned", "deleted", deleted, "retention_days", days)
	}
}

// List 分页查询（正文为预览，全文走 Get）。
func (s *UserMessageLogService) List(ctx context.Context, filter *UserMessageLogFilter) (*UserMessageLogList, error) {
	return s.repo.List(ctx, filter)
}

// Get 查询单条全文。
func (s *UserMessageLogService) Get(ctx context.Context, id int64) (*UserMessageLog, error) {
	return s.repo.Get(ctx, id)
}

// ErrUserMessageViewerNotFound 授权记录不存在。
var ErrUserMessageViewerNotFound = errors.New("user message viewer not found")

// ErrUserMessageViewerDenied 未被授权查看用户消息。
var ErrUserMessageViewerDenied = errors.New("no permission to view user messages")

// UserMessageViewerEntry 是一条「用户可看某分组」授权。
type UserMessageViewerEntry struct {
	ID        int64     `json:"id"`
	UserID    int64     `json:"user_id"`
	Username  string    `json:"username"`
	UserEmail string    `json:"user_email"`
	GroupID   int64     `json:"group_id"`
	GroupName string    `json:"group_name"`
	CreatedAt time.Time `json:"created_at"`
}

// GroupUsageRanking 是分组内按用户的用量排行行。
type GroupUsageRanking struct {
	UserID      int64   `json:"user_id"`
	Username    string  `json:"username"`
	UserEmail   string  `json:"user_email"`
	Requests    int64   `json:"requests"`
	InputTokens int64   `json:"input_tokens"`
	OutputTokens int64  `json:"output_tokens"`
	CacheRead   int64   `json:"cache_read_tokens"`
	TotalCost   float64 `json:"total_cost"`
}

// UserMessageViewerRepository 由 repository 层实现。
type UserMessageViewerRepository interface {
	List(ctx context.Context) ([]*UserMessageViewerEntry, error)
	GroupsForUser(ctx context.Context, userID int64) ([]int64, error)
	Grant(ctx context.Context, userID, groupID, createdBy int64) error
	Revoke(ctx context.Context, id int64) error
	RankGroupUsage(ctx context.Context, groupID int64, since time.Time, limit int) ([]*GroupUsageRanking, error)
	GroupNames(ctx context.Context, ids []int64) ([]*ViewerGroupInfo, error)
}

// ViewerGroups 返回用户被授权可见的分组 ID 集。
func (s *UserMessageLogService) ViewerGroups(ctx context.Context, userID int64) ([]int64, error) {
	if s == nil || s.viewerRepo == nil {
		return nil, nil
	}
	return s.viewerRepo.GroupsForUser(ctx, userID)
}

// ListViewers 全量授权清单（管理）。
func (s *UserMessageLogService) ListViewers(ctx context.Context) ([]*UserMessageViewerEntry, error) {
	return s.viewerRepo.List(ctx)
}

// GrantViewer 授予用户查看某分组。
func (s *UserMessageLogService) GrantViewer(ctx context.Context, userID, groupID, createdBy int64) error {
	return s.viewerRepo.Grant(ctx, userID, groupID, createdBy)
}

// RevokeViewer 收回授权。
func (s *UserMessageLogService) RevokeViewer(ctx context.Context, id int64) error {
	return s.viewerRepo.Revoke(ctx, id)
}

// ListForViewer 查看者视角的消息列表：分组范围强制收敛到其授权集合。
func (s *UserMessageLogService) ListForViewer(ctx context.Context, userID int64, filter *UserMessageLogFilter) (*UserMessageLogList, error) {
	allowed, err := s.ViewerGroups(ctx, userID)
	if err != nil {
		return nil, err
	}
	if len(allowed) == 0 {
		return nil, ErrUserMessageViewerDenied
	}
	allowedSet := make(map[int64]bool, len(allowed))
	for _, id := range allowed {
		allowedSet[id] = true
	}
	if filter.GroupID != nil {
		if !allowedSet[*filter.GroupID] {
			return nil, ErrUserMessageViewerDenied
		}
	} else if len(allowed) == 1 {
		filter.GroupID = &allowed[0]
	} else {
		// 多分组授权且未指定：跨授权分组聚合查询，用 IN 条件。
		filter.AllowedGroupIDs = allowed
	}
	return s.repo.List(ctx, filter)
}

// GetForViewer 查看者视角的单条全文（必须落在授权分组内）。
func (s *UserMessageLogService) GetForViewer(ctx context.Context, userID, id int64) (*UserMessageLog, error) {
	allowed, err := s.ViewerGroups(ctx, userID)
	if err != nil {
		return nil, err
	}
	if len(allowed) == 0 {
		return nil, ErrUserMessageViewerDenied
	}
	item, err := s.repo.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	for _, gid := range allowed {
		if item.GroupID == gid {
			return item, nil
		}
	}
	return nil, ErrUserMessageViewerDenied
}

// RankGroupUsage 分组用量排行（admin 任意组；viewer 由 handler 校验授权）。
func (s *UserMessageLogService) RankGroupUsage(ctx context.Context, groupID int64, days int) ([]*GroupUsageRanking, error) {
	if days <= 0 || days > 90 {
		days = 7
	}
	return s.viewerRepo.RankGroupUsage(ctx, groupID, time.Now().AddDate(0, 0, -days), 50)
}

// ViewerGroupInfo 是查看者可见分组的 id+name。
type ViewerGroupInfo struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

// ViewGroupNames 按分组 ID 集取名称。
func (s *UserMessageLogService) ViewGroupNames(ctx context.Context, ids []int64) ([]*ViewerGroupInfo, error) {
	if len(ids) == 0 {
		return []*ViewerGroupInfo{}, nil
	}
	return s.viewerRepo.GroupNames(ctx, ids)
}
