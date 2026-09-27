export default {
  modelTrace: {
    title: 'Codex 降智检测',
    description: '基于随机整数分布指纹自建模型指纹库，检测上游实际服务模型与请求模型是否一致',

    // 设置
    settings: '检测设置',
    models: '候选模型',
    modelsHelp: '参与建库与检测的模型（至少 2 个）；判定依赖跨模型区分度',
    bankRepeats: '建库每模型采样数',
    bankRepeatsHelp: '每模型向各官号轮换采集的回答数（最少 8，建议 12+）',
    detectRepeats: '检测回答数',
    concurrency: '并发探测数',
    probeTimeout: '单次探测超时（秒）',
    requestGap: '相邻探测间隔（毫秒）',
    save: '保存设置',
    saving: '保存中...',
    needTwoModels: '候选模型至少选择 2 个',

    // 账号选择
    accounts: '探测账号',
    accountsHelp: '从分组中选择参与探测的 OpenAI OAuth 官号；探测请求走账号自身出口',
    selectGroup: '选择分组',
    allGroups: '全部分组',
    selectedCount: '已选 {count} 个账号',

    // 运行
    run: '运行任务',
    runHelp: '建库：为每个候选模型采集样本并重建指纹库（覆盖旧库）；检测：对每个（账号×模型）组合判定上游实际服务的模型',
    runBank: '建库',
    runDetect: '检测',
    taskRunning: '任务运行中',
    kindBank: '建库',
    kindDetect: '检测',
    noTask: '暂无任务（建库和检测不可同时运行）',

    // 指纹库
    bankStatus: '指纹库',
    bankEmpty: '尚未建库',
    builtAt: '建库时间',
    bankModels: '库内模型',
    samples: '建库样本数',
    totalSamples: '累计样本',
    calibration: '校准 β（按回答数）',
    orderedWeight: '顺序特征权重',

    // 结果
    results: '检测结果',
    refresh: '刷新',
    time: '时间',
    kind: '类型',
    account: '账号',
    model: '请求模型',
    verdict: '判定模型',
    match: '匹配',
    matchOk: '一致',
    mismatch: '不一致',
    probability: '置信度',
    validRuns: '有效/失败',
    avgLatency: '平均耗时',
    reasons: '原因',
    noResults: '暂无结果',

    // 样本
    samplesCard: '建库样本',
    allModels: '全部模型',
    expectedCount: '期望/实得',
    latency: '耗时',
    numbers: '数字序列',
    deleteSamples: '删除该模型样本',
    confirmDelete: '确定删除模型 {model} 的全部建库样本？',
    deleted: '已删除 {count} 条样本',
    noSamples: '暂无样本'
  }
}
