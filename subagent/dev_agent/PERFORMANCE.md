# PERFORMANCE.md — 性能优化规范

> 版本：1.0.0 | 关联：Dev-Agent SKILL.md v5.2.0 | 继承：Google performance-optimization

---

## 概述

本文档定义 Shrimp Dev-Agent 的性能优化规范，涵盖：
- 性能瓶颈识别（热点分析）
- 资源控制（内存、连接池、并发限制）
- 缓存策略
- 异步处理
- 常见性能问题预防（连接池泄漏、goroutine 泄漏、N+1 查询）
- Metrics 打点规范

**核心原则**：性能优化必须在设计阶段考虑，而非事后补救。所有关键路径必须可观测（metrics + trace + log）。

---

## 1. 性能瓶颈识别

### 1.1 热点分析规范

**识别维度**：
- CPU 热点：`pprof` CPU profile（`go tool pprof http://localhost:6060/debug/pprof/profile?seconds=30`）
- 内存热点：`pprof` heap profile（`go tool pprof http://localhost:6060/debug/pprof/heap`）
- Goroutine 热点：`pprof` goroutine profile + `runtime.NumGoroutine()`
- I/O 热点：网络延迟、数据库查询耗时、锁竞争

**Go 项目必用**：
```go
import _ "net/http/pprof"

// 匿名导入，在 debug server 上暴露 /debug/pprof/* 路由
// 仅在 debug 模式或内部环境启用，禁止在生产环境暴露
```

**pprof 分析流程**：
```
1. 开启 debug 端口：go tool pprof -http=:8080 http://localhost:6060/debug/pprof/profile?seconds=30
2. 打开浏览器 http://localhost:8080
3. 查看 Top 视图：定位消耗最大的函数
4. 查看 Flame Graph：定位调用链路热点
5. 查看 Goroutine：检测 goroutine 泄漏
```

### 1.2 Bytertc/Signal 特定热点

| 场景 | 热点指标 | 排查工具 |
|------|---------|---------|
| 连接池耗尽 | `conn_pool_available == 0` | Argos metrics + 日志 |
| Goroutine 泄漏 | `runtime.NumGoroutine()` 持续增长 | pprof goroutine profile |
| 消息堆积 | `queue_depth` 持续上升 | Argos metrics |
| GC 压力 | `go_gc_pause_seconds` > 100ms | pprof heap |

### 1.3 Metrics 上报（关键路径）

**必打点位置**：
```go
// 1. 入口层：请求开始
metrics.Counter("request_total", "service", svc, "method", m, "status", "200")

// 2. 关键路径：业务逻辑
metrics.Timer("business_logic_duration", "op", "dedup", "engine", "new")

// 3. 出口层：请求结束
metrics.Histogram("request_duration_ms", "service", svc, "method", m)
```

---

## 2. 资源控制

### 2.1 内存控制

**内存上限**：
```go
// 设置 GOGC（垃圾回收阈值）
// 内存敏感服务建议：GOGC=50（内存达到上限 50% 时触发 GC）
// 或使用 debug.SetGCPercent(50)

// 设置最大 P（处理器）数量，防止过载
debug.SetMaxThreads(10000)
```

**内存泄漏检测**：
```go
// 定期 dump heap 对比
// go tool pprof -diff_base=heap_before.pb http://localhost:6060/debug/pprof/heap

// 常见泄漏模式：
// 1. 全局 map 持续写入不清理
// 2. slice append 无限增长
// 3. timer/ ticker 未 stop
// 4. channel 无缓冲且无人接收
```

### 2.2 连接池控制

**数据库连接池**：
```go
// ByteRPC / Kratos / GORM 均支持连接池配置
// 必须设置以下参数：

type DBConfig struct {
    MaxOpenConns    int   // 最大打开连接数 = = QPS * 平均响应时间(s)
    MaxIdleConns    int   // 建议 = MaxOpenConns / 3
    ConnMaxLifetime time.Duration // 连接最大存活时间，建议 30min
    ConnMaxIdleTime time.Duration // 空闲连接最大存活，建议 5min
}

// 监控连接池状态：
metrics.Gauge("db_conn_pool_active", db.Stats().OpenConnections)
metrics.Gauge("db_conn_pool_idle", db.Stats().IdleConnections)
metrics.Gauge("db_conn_pool_wait", db.Stats().WaitCount)
```

**RPC 连接池（ByteRPC）**：
```go
// 连接池配置
type ClientConfig struct {
    MaxIdleConns        int
    MaxConnsPerHost     int  // 建议 = QPS / 100
    MaxConnWaitTimeout  time.Second
    MaxConnAge          time.Second // 连接最大存活
    KeepAlive           time.Second
}

// 监控连接池状态：
metrics.Gauge("rpcc_pool_active", pool.Stats().ActiveConns)
metrics.Gauge("rpcc_pool_idle", pool.Stats().IdleConns)
metrics.Counter("rpcc_pool_wait_total", "reason", "timeout")
```

**连接池泄漏预防**：
```
泄漏信号：
- 连接池活跃连接数持续增长
- 连接池可用连接数 = 0 且等待超时
- 错误日志出现 "connection pool exhausted"

预防措施：
1. 所有连接必须 defer Close() 或使用 context timeout
2. 禁止在 defer 中使用循环引用导致无法释放的闭包
3. 连接使用后必须归还（Close 不等于归还，必须放回池）
4. 每次 RPC 调用必须设置 timeout（禁止无 timeout 的 context.Background()）
```

### 2.3 并发控制

**信号量模式**：
```go
// 限制并发数，防止资源耗尽
var sem = make(chan struct{}, 100) // 最多 100 个并发

func process(item Item) {
    sem <- struct{}{}
    defer func() { <-sem }()
    // ... 处理逻辑
}

// 推荐：使用 golang.org/x/sync/semaphore
var sem = semaphore.NewWeighted(100)

func process(ctx context.Context, item Item) error {
    if err := sem.Acquire(ctx, 1); err != nil {
        return err
    }
    defer sem.Release(1)
    // ... 处理逻辑
}
```

**Worker Pool 模式**：
```go
// 固定数量 worker，避免 goroutine 爆炸
func StartWorkerPool(ctx context.Context, numWorkers int, jobs <-chan Job) {
    var wg sync.WaitGroup
    for i := 0; i < numWorkers; i++ {
        wg.Add(1)
        go func(workerID int) {
            defer wg.Done()
            for job := range jobs {
                processJob(workerID, job)
            }
        }(i)
    }
    wg.Wait()
}
```

**限流（Rate Limiting）**：
```go
// 令牌桶限流
var limiter = rate.NewLimiter(1000, 1000) // QPS=1000，burst=1000

func HandleRequest(ctx context.Context, req *Request) error {
    if !limiter.Allow() {
        metrics.Counter("rate_limit_exceeded_total", "path", path)
        return ErrRateLimitExceeded
    }
    // ... 处理逻辑
}
```

---

## 3. 缓存策略

### 3.1 缓存设计原则

| 维度 | 规范 |
|------|------|
| 缓存粒度 | 尽量粗（减少 key 数量），避免 N+1 |
| 缓存失效 | 必须有 TTL，禁止无限增长 |
| 缓存容量 | 必须有最大容量限制（LRU/Evict） |
| 缓存穿透 | 缓存空值（TTL 短）或布隆过滤器 |
| 缓存雪崩 | TTL 随机抖动 + 熔断降级 |

### 3.2 本地缓存（Gcache）：

```go
// 使用 github.com/bluele/gcache 或内部 bigcache
import "github.com/bluele/gcache"

var cache = gcache.New(10000).
    LoaderFunc(func(key string) (interface{}, error) {
        return loadFromDB(key)
    }).
    EvictionPolicy(gcache.LRU).
    Build()

// 使用示例
val, err := cache.Get("user:123")
if err == gcache.ErrKeyNotFound {
    // 缓存未命中，LoaderFunc 自动加载
}
```

### 3.3 分布式缓存（Redis/Etcd）：

```go
// Redis 缓存最佳实践
func GetUserCache(ctx context.Context, userID string) (*User, error) {
    key := fmt.Sprintf("user:%s", userID)
    
    // 1. 尝试缓存
    data, err := redis.Get(ctx, key).Bytes()
    if err == nil {
        var user User
        json.Unmarshal(data, &user)
        metrics.Counter("cache_hit_total", "cache", "redis", "type", "user")
        return &user, nil
    }
    
    // 2. 缓存未命中
    metrics.Counter("cache_miss_total", "cache", "redis", "type", "user")
    user, err := loadFromDB(userID)
    if err != nil {
        return nil, err
    }
    
    // 3. 写入缓存（异步或同步，视场景）
    go func() {
        data, _ := json.Marshal(user)
        redis.Set(ctx, key, data, 5*time.Minute)
    }()
    
    return user, nil
}
```

### 3.4 缓存与 API 设计联动

**API Contract-First 中的缓存设计**：
```yaml
# docs/api/user_service_get_user_cache.yaml

caching:
  strategy: "cache-aside"
  ttl: 300  # 5分钟
  max_stale: 60  # 允许 stale 数据 60s
  
  cache_key_pattern: "user:{user_id}"
  
  invalidation:
    - event: "user.update"
      action: "delete user:{user_id}"
    - event: "user.delete"
      action: "delete user:{user_id}"

error_handling:
  cache_error: "log and continue (fallback to source)"
  source_error: "return error to client"
```

---

## 4. 异步处理

### 4.1 异步 vs 同步选择

| 场景 | 推荐方式 | 原因 |
|------|---------|------|
| 非关键路径（日志、审计） | 异步 fire-and-forget | 不阻塞主流程 |
| 可延迟处理（通知、刷新缓存） | 异步 + 重试 | 削峰填谷 |
| 强一致性要求 | 同步 | 异步无法保证 |
| 下游依赖超时敏感 | 同步 + 超时控制 | 避免级联失败 |
| 上游请求超时短 | 同步 + 熔断 | 保护上游 |

### 4.2 异步任务实现

**Go 队列模式**：
```go
type AsyncTask struct {
    ID      string
    Payload interface{}
    Retries int
}

var taskQueue = make(chan AsyncTask, 10000)

// 生产者
func EnqueueTask(ctx context.Context, task AsyncTask) error {
    select {
    case taskQueue <- task:
        metrics.Incr("async_task_queued_total", "type", task.Type)
        return nil
    case <-ctx.Done():
        return ctx.Err()
    default:
        metrics.Incr("async_task_dropped_total", "type", task.Type)
        return ErrQueueFull
    }
}

// 消费者（Worker Pool）
func StartAsyncWorkers(ctx context.Context, numWorkers int) {
    for i := 0; i < numWorkers; i++ {
        go func(workerID int) {
            for task := range taskQueue {
                if err := processTask(ctx, task); err != nil {
                    retryTask(task) // 放回队列或死信队列
                }
                metrics.Incr("async_task_completed_total", "worker", fmt.Sprintf("%d", workerID))
            }
        }(i)
    }
}
```

**Kafka/EventBus 异步**：
```go
// 生产者：异步发送，不阻塞主流程
func SendEvent(ctx context.Context, event *Event) error {
    // 包装为非阻塞发送
    go func() {
        pubCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
        defer cancel()
        if err := eventBus.Publish(pubCtx, event.Topic, event); err != nil {
            metrics.Counter("event_publish_failed_total", "topic", event.Topic)
            // 放死信队列
            deadLetterQueue.Push(event)
        }
    }()
    return nil // 主流程不等待
}
```

### 4.3 Goroutine 泄漏预防

**泄漏检测**：
```go
// 在 debug 接口暴露 goroutine 数量
http.HandleFunc("/debug/goroutines", func(w http.ResponseWriter, r *http.Request) {
    fmt.Fprintf(w, "NumGoroutine: %d\n", runtime.NumGoroutine())
})

// 监控告警
metrics.Gauge("goroutine_count", "service", svc, "instance", instance)
```

**泄漏模式与预防**：
```
模式1：channel 无人接收
  预防：确保 channel 有接收方，使用 buffered channel 或 select+default

模式2：goroutine 阻塞在 channel
  预防：所有 channel 操作必须配合 context timeout

模式3：定时器未停止
  预防：timer.Stop()，或使用 sync.Once 确保证只执行一次

模式4：闭包引用外部变量导致循环引用
  预防：闭包参数显式传递，禁止隐式捕获

反模式示例：
  go func() {
      for {
          data := <-ch  // ch 永不关闭，goroutine 泄漏
          process(data)
      }
  }()

正确模式：
  go func() {
      for {
          select {
          case data := <-ch:
              process(data)
          case <-ctx.Done():
              return  // 使用 context 控制生命周期
          }
      }
  }()
```

---

## 5. 常见性能问题预防

### 5.1 连接池泄漏

**典型场景**：ByteRTC Signal 服务中，RPC 连接未正确释放

**代码检查**：
```go
// 错误：context 没有超时
resp, err := client.Call(ctx, req) // ctx = context.Background()

// 正确：所有调用必须设置超时
ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
defer cancel()
resp, err := client.Call(ctx, req)

// 错误：defer 在循环中
for {
    conn, err := client.GetConn()  // 获取新连接
    defer conn.Close()             // defer 堆积直到函数结束
    // ...
}

// 正确：循环中正确管理连接生命周期
for {
    conn, err := client.GetConn()
    if err != nil { return err }
    if err := conn.Close(); err != nil { /* handle */ }
    // ...
}
```

### 5.2 Goroutine 泄漏

**ByteRTC/Signal 典型泄漏场景**：
```go
// 场景1：WebSocket 连接未关闭
func (s *Server) HandleWS(conn *websocket.Conn) {
    for {
        msg, err := conn.ReadMessage()
        if err != nil {
            // 错误：没有 Close 连接
            return
        }
        s.process(msg)
    }
}

// 正确：使用 defer 确保关闭
func (s *Server) HandleWS(conn *websocket.Conn) {
    defer conn.Close() // 确保退出时关闭
    for {
        select {
        case msg, err := conn.ReadMessage():
            if err != nil { return }
            s.process(msg)
        case <-conn.Context().Done():
            return
        }
    }
}

// 场景2：Signal 内部定时器泄漏
func (s *Signal) Start() {
    s.ticker = time.NewTicker(time.Second)
    go func() {
        for {
            <-s.ticker.C  // 泄漏：函数退出后 ticker 仍在运行
            s.tick()
        }
    }()
}

// 正确：使用 Stop + context 取消
func (s *Signal) Start(ctx context.Context) {
    ctx, cancel := context.WithCancel(ctx)
    s.cancel = cancel
    s.ticker = time.NewTicker(time.Second)
    go func() {
        defer s.ticker.Stop()
        for {
            select {
            case <-s.ticker.C:
                s.tick()
            case <-ctx.Done():
                return
            }
        }
    }()
}
```

### 5.3 N+1 查询

**典型场景**：数据库批量查询分散为单条查询

**反模式**：
```go
// 反模式：N+1 查询
type User struct {
    ID   int
    Name string
}

users, _ := db.Query("SELECT * FROM users")
for _, user := range users {
    // 每个 user 触发一次 dept 查询 = N+1
    dept, _ := db.Query("SELECT * FROM dept WHERE id = ?", user.DeptID)
    user.Dept = dept
}
```

**正确模式**：
```go
// 正确：批量查询
users, _ := db.Query("SELECT * FROM users")

// 收集所有 dept_id
deptIDs := make([]int, len(users))
for i, u := range users {
    deptIDs[i] = u.DeptID
}

// 一次查询获取所有 dept
depts, _ := db.Query("SELECT * FROM dept WHERE id IN (?)", deptIDs)
deptMap := make(map[int]*Dept)
for _, d := range depts {
    deptMap[d.ID] = d
}

// 组装
for _, u := range users {
    u.Dept = deptMap[u.DeptID]
}
```

**缓存优化**：
```go
// 最佳：缓存预热 + 批量查询
func GetUsersWithDepts(userIDs []int) ([]*User, error) {
    // 1. 批量获取用户
    users, err := db.Query("SELECT * FROM users WHERE id IN (?)", userIDs)
    if err != nil { return nil, err }
    
    // 2. 获取缺失的 dept（检查缓存）
    missingDeptIDs := []int{}
    for _, u := range users {
        if _, ok := deptCache.Get(u.DeptID); !ok {
            missingDeptIDs = append(missingDeptIDs, u.DeptID)
        }
    }
    
    // 3. 批量获取缺失的 dept
    if len(missingDeptIDs) > 0 {
        depts, _ := db.Query("SELECT * FROM dept WHERE id IN (?)", missingDeptIDs)
        for _, d := range depts {
            deptCache.Set(d.ID, d) // 写入缓存
        }
    }
    
    // 4. 组装（从缓存）
    for _, u := range users {
        u.Dept, _ = deptCache.Get(u.DeptID)
    }
    return users, nil
}
```

---

## 6. Metrics 打点规范

### 6.1 字节内部 Metrics 规范

**必须使用 Metricx/BytedMetrics 库**（内部 SDK）：
```go
import "code.byted.org/gopkg/metricx/v2"

// 推荐：使用 WithTags 避免 label 重复创建
metricx.GetCounter("request_total",
    metricx.WithTags(map[string]string{"service": svc, "method": method}),
).Incr()

metricx.GetTimer("request_duration",
    metricx.WithTags(map[string]string{"service": svc, "method": method}),
).Since(start)
```

### 6.2 Metrics 打点黄金四问

```
1. 这个指标衡量什么？（业务价值）
2. 这个指标在什么阈值下需要告警？
3. 告警后谁处理？处理流程是什么？
4. 这个指标与其他指标的关系是什么？
```

### 6.3 关键路径打点清单

**服务入口**：
```go
// 请求计数
metricx.GetCounter("gateway_request_total",
    metricx.WithTags(map[string]string{
        "service":  svc,
        "method":   method,
        "status":   status, // 200/400/500
    }),
).Incr()

// 请求延迟
metricx.GetTimer("gateway_request_duration_ms",
    metricx.WithTags(map[string]string{"service": svc, "method": method}),
).Since(start)
```

**业务逻辑层**：
```go
// 缓存命中率
metricx.GetCounter("cache_hit_total",
    metricx.WithTags(map[string]string{"cache": "redis", "key_type": keyType}),
).Incr()

metricx.GetCounter("cache_miss_total",
    metricx.WithTags(map[string]string{"cache": "redis", "key_type": keyType}),
).Incr()

// 连接池状态
metricx.GetGauge("conn_pool_available",
    metricx.WithTags(map[string]string{"pool": "rpc"}),
).Set(float64(pool.Available()))

metricx.GetGauge("conn_pool_waiting",
    metricx.WithTags(map[string]string{"pool": "rpc"}),
).Set(float64(pool.Waiting()))

// Goroutine 数量（调试用）
metricx.GetGauge("goroutine_count",
    metricx.WithTags(map[string]string{"service": svc}),
).Set(float64(runtime.NumGoroutine()))
```

**下游调用**：
```go
// RPC 调用
metricx.GetTimer("downstream_rpc_duration_ms",
    metricx.WithTags(map[string]string{
        "downstream": downstream,
        "method":     method,
    }),
).Since(start)

// 错误计数
metricx.GetCounter("downstream_rpc_error_total",
    metricx.WithTags(map[string]string{
        "downstream": downstream,
        "error_code": errorCode,
    }),
).Incr()
```

### 6.4 Metrics 与 API 设计联动

**API Schema 中的 metrics 定义**：
```yaml
# docs/api/dedup_engine_metrics.yaml

metrics:
  request_total:
    type: counter
    description: "Dedup 请求总数"
    labels:
      - service
      - method
      - status
    alert_threshold: "rate(request_total{service=dedup,status=500}) > 10/min"
    
  request_duration_ms:
    type: histogram
    description: "Dedup 请求延迟分布"
    labels:
      - service
      - method
    buckets: [5, 10, 25, 50, 100, 250, 500, 1000]
    alert_threshold: "p99(request_duration_ms) > 500"
    
  cache_hit_rate:
    type: gauge
    description: "缓存命中率"
    labels:
      - cache_type
    alert_threshold: "cache_hit_rate < 0.8"
    
  goroutine_count:
    type: gauge
    description: "Goroutine 数量（用于泄漏检测）"
    alert_threshold: "goroutine_count > 10000 OR delta(goroutine_count) > 1000/min"
```

---

## 7. 性能优化检查清单

### 开发阶段必检

```
[ ] 所有 RPC/HTTP 调用都有 timeout（禁止无 timeout 的 context.Background()）
[ ] 连接池配置正确（MaxOpenConns / MaxConnsPerHost）
[ ] 循环中没有 defer（连接资源泄漏风险）
[ ] Goroutine 有受控生命周期（context / channel / sync）
[ ] 定时器有 Stop() 或使用 context 取消
[ ] 批量查询替代 N+1 查询
[ ] 热点路径有缓存（TTL + 最大容量）
[ ] 所有关键路径有 metrics 打点
[ ] 错误路径有 metrics 打点（错误计数）
```

### Code Review 必检

```
[ ] 无 hardcoded connection string / credentials
[ ] defer 不会在循环中堆积
[ ] channel 操作不会导致 goroutine 永久阻塞
[ ] 内存增长有上限（无无限增长的 slice/map）
[ ] 定时器在退出路径上有 Stop()
[ ] 连接池有上限保护（不会无限创建）
[ ] 并发控制有信号量或 worker pool
[ ] N+1 查询已消除
[ ] metrics 打点覆盖关键路径
[ ] 无阻塞的 channel send/receive（select + default）
```

### 上线前必检

```
[ ] pprof 已开启（debug 环境）
[ ] metrics 告警阈值已配置
[ ] 连接池监控已配置
[ ] Goroutine 数量告警已配置
[ ] 缓存命中率告警已配置
[ ] 压测结果满足 SLA（延迟 / QPS / 错误率）
```

---

## 8. 性能优化与 API Design 联动

**API Contract-First Design 中的性能约束**：

```yaml
# docs/api/{service}_performance.yaml

performance:
  # 必填：每个接口的 SLA
  sla:
    latency_p50_ms: 10
    latency_p99_ms: 100
    max_qps: 10000
    max_error_rate: 0.01  # 1%
    
  # 必填：超时配置
  timeouts:
    read_timeout_ms: 3000
    write_timeout_ms: 3000
    idle_timeout_ms: 10000
    
  # 必填：限流配置
  rate_limiting:
    enabled: true
    requests_per_second: 10000
    burst: 20000
    
  # 必填：缓存配置
  caching:
    enabled: true
    ttl_seconds: 300
    max_size_mb: 1024
    
  # 必填：资源限制
  resource_limits:
    max_connections: 1000
    max_concurrent_requests: 500
    max_request_size_kb: 1024
    max_response_size_kb: 10240
```

---

## 参考

- Google Performance Optimization Best Practices
- ByteRTC Signal Performance Runbook（内部）
- Metricx SDK Documentation（内部）
- Dev-Agent SKILL.md v5.2.0
- Dev-Agent SECURITY.md
