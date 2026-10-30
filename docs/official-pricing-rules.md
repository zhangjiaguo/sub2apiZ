# 官方价格补充规则

普通价卡继续保存美元单价和分组倍率。`internal/pkg/pricingrules` 处理输出长度分档、思考模式、显式缓存读取和十二小时缓存写入这些附加条件；规则在Token分项计算后、峰谷倍率之前生效。

进程启动时读取 `OFFICIAL_PRICING_RULES_FILE` 指定的JSON文件，缺省路径为 `/app/data/official_pricing_rules.json`。缺省文件不存在时不启用补充规则；显式配置的文件缺失、字段不支持或数据无效时停止启动。文件以数据卷持久化，更新后重启服务发布新快照。

JSON格式版本为1，`models` 按不区分大小写的模型名匹配，兼容复合Key前缀。每个型号可以配置 `output_tiers`、`thinking_output_multiplier`、`thinking_default`、`cache_write_12h_multiplier`、`explicit_cache_read_multiplier` 和 `holiday_off_peak`。档位按顺序选择第一个同时满足输入、输出下限的条目，输入量包含缓存读取及创建。

OpenAI上游的 `input_tokens_details.cache_write_tokens_details.ttl_12h_tokens` 是写入总量的子集，不是额外输入量。明细超过总写入量时截断；未报告明细的部分按短期写入价计算。Responses、Chat转换和WebSocket每轮快照保留标准TTL明细。思考Token本身已经包含在输出量中，模式只决定输出单价。

Qwen显式缓存由支持位置的 `cache_control.type=ephemeral` 标记识别，`metadata` 内同名字段不参与判断。百炼的 `cache_creation_input_tokens` 嵌套别名与OpenAI的 `cache_write_tokens` 均可传入创建量。模式信息与计数随异步结算快照传递，内部事实不新增客户端协议字段。

`china_holidays` 按年份保存国务院公布的完整放假日期，使用北京时间确定日期，保留原峰谷时段和周末豁免。启用了节假日低峰的型号，在尚未配置日历的年份先按低峰价计费，避免未知日期多扣费；管理员须在官方发布下一年安排后更新文件。2026年日历依据[国务院办公厅通知](https://www.beijing.gov.cn/cs/gncs/zcwj/202603/t20260327_4568275.html)。

单价和规则应来自同一报价快照。国产报价以人民币入选，并按运营者确认的固定汇率折算美元；三种Qwen3-Embedding开源型号没有可比的官方每Token价格，应标注自定义价。供应商资源小时价不能直接当作Token单价。

回归入口为 `go test -tags unit ./internal/service ./internal/service/openai_ws_v2 ./internal/pkg/apicompat ./internal/pkg/pricingrules`。`testdata/official_pricing` 只包含公开价格和固定金额算例，不包含用户、账号或凭据。

参考：[OpenAI缓存协议](https://developers.openai.com/api/docs/guides/prompt-caching)、[百炼缓存](https://help.aliyun.com/zh/model-studio/context-cache)、[GLM官价](https://docs.bigmodel.cn/cn/guide/start/pricing.md)。
