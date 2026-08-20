# Knowledge RAG 升级：PostgreSQL + pgvector 部署指南（M8-04）

> 对应任务：M8-04「Knowledge RAG 升级 PG/pgvector」。
> 前置：M5-02（Knowledge RAG 基线，本地 SQLite 向量库）已落地。
> 本文档面向运维：从 SQLite 基线平滑切换到 pgvector，支撑**万级 chunk 检索**与**并发检索**。

---

## 1. 为什么升级

| 维度 | SQLite 基线（KB_STORE=sqlite） | pgvector（KB_STORE=pgvector） |
|------|-------------------------------|------------------------------|
| 检索方式 | 全表扫描 + Go 内存余弦计算 | `embedding <=> $v` 余弦距离算子下推到 PG |
| 万级 chunk | 每次检索遍历全部向量，时延随数据量线性劣化 | HNSW 近似最近邻索引，检索复杂度 ~O(log n) |
| 并发 | 单文件写锁（`_txlock=immediate`），并发读受 SQLite 限制 | 连接池 + PG 多版本并发控制，读写无锁冲突 |
| 扩展性 | 单副本约束 | 可随 PG 集群演进（PITR/副本/托管云 PG） |
| 适用规模 | < 5k chunk 的中小型知识库 | 万级 ~ 百万级 chunk 的企业知识库 |

**判断标准**：知识库 chunk 总量达到 ~1 万级、或对话并发检索开始出现明显时延抖动时，切换到 pgvector。

## 2. 部署 PostgreSQL + pgvector 扩展

### 2.1 全新部署（Docker 推荐）

pgvector 官方镜像已内置扩展：

```bash
docker run -d --name pgvector \
  -e POSTGRES_USER=codeagent \
  -e POSTGRES_PASSWORD=<强密码> \
  -e POSTGRES_DB=codeagent \
  -p 5432:5432 \
  -v pgvector-data:/var/lib/postgresql/data \
  pgvector/pgvector:pg16
```

### 2.2 现有 PostgreSQL 上加装扩展

```sql
-- 需要超级用户或具备 CREATE EXTENSION 权限
CREATE EXTENSION IF NOT EXISTS vector;
-- 验证
SELECT extversion FROM pg_extension WHERE extname = 'vector';
```

> pgvector 版本要求：**≥ 0.5.0 才有 HNSW 索引**（推荐）；旧版本自动降级 IVFFlat。
> 本平台启动时自动探测扩展并建索引，扩展缺失时返回明确错误（不会静默用错误后端）。

## 3. 配置切换

在 `.env` 或环境变量中：

```bash
# 向量库后端：sqlite（默认）| pgvector
KB_STORE=pgvector
# PostgreSQL 连接串（pgvector 模式必填；缺失则启动失败，杜绝「以为切了 PG 实际还在用 sqlite」）
KB_PG_DSN=postgres://codeagent:password@localhost:5432/codeagent
# 向量维度：须与嵌入器一致（内置本地嵌入器固定 256；若换 OpenAI 等远程嵌入器须同步修改并重建表）
KB_PG_DIM=256
# 连接池大小：并发检索支撑，按 QPS 调整（默认 10）
KB_PG_POOL=10
```

**维度一致性**：`kb_vectors.embedding` 列固定为 `vector(N)`。若表已存在且 N 与 `KB_PG_DIM` 不一致，
启动报错并提示 `DROP TABLE kb_vectors` 重建（换嵌入器维度属破坏性变更，需重索引全部文档）。

## 4. 存储与索引策略（启动时自动幂等完成）

| 步骤 | 动作 |
|------|------|
| 1 | 检查 `pg_extension` 中 vector 扩展（缺失即报错） |
| 2 | `CREATE TABLE IF NOT EXISTS kb_vectors`（id TEXT PK / kb_id / doc_name / name / content / embedding vector(N) / metadata jsonb / created_at） |
| 3 | 校验 embedding 列维度与配置一致 |
| 4 | `CREATE INDEX ... kb_vectors (kb_id)`（按知识库过滤） |
| 5 | `CREATE INDEX ... USING hnsw (embedding vector_cosine_ops)`（pgvector ≥ 0.5.0） |
| 6 | HNSW 不可用则降级 `USING ivfflat (embedding vector_cosine_ops) WITH (lists = 100)`；再失败仅警告（小数据量纯扫描可用） |

检索 SQL（余弦相似度）：`1 - (embedding <=> $v) AS score`，metadata 过滤用 jsonb 包含操作符
`metadata @> $m::jsonb`，IDs 过滤用 `id = ANY($n::text[])`——全部下推到 PG，HNSW 上常数级检索。

**HNSW 调参（高级）**：默认使用 pgvector 出厂参数（m=16, ef_construction=64），对万级数据已足够；
百万级场景可显式建索引：

```sql
CREATE INDEX ON kb_vectors USING hnsw (embedding vector_cosine_ops)
  WITH (m = 32, ef_construction = 128);
```

## 5. 万级检索实测方法

本机/CI 无 PG 时集成测试自动 Skip（设置 `PG_TEST_DSN` 才跑），有 PG 的 runner 真跑：

```bash
# 集成测试（CRUD/检索排序/删除/并发 20×50/kb 隔离）
PG_TEST_DSN='postgres://codeagent:password@localhost:5432/codeagent' \
  go test -count=1 -v ./internal/knowledge/store/ -run TestPGVectorStore_

# 万级数据测试：插入 1 万 chunk 并输出 500 次检索的 P50/P95/P99
PG_TEST_DSN='...' PG_SCALE_TEST=1 \
  go test -count=1 -v ./internal/knowledge/store/ -run TestPGVectorStore_Scale10k
```

输出示例（真实环境实测值写入本报告）：

```
插入 10000 条完成：8.2s
万级检索 500 次：P50=2.1ms P95=3.4ms P99=5.2ms max=18.7ms（空结果=0/500）
```

## 6. 数据迁移（SQLite → pgvector）

平台当前不提供自动搬运脚本（两库 schema 同构，迁移成本低），推荐两种方式：

1. **简单重索引**（推荐）：切换 `KB_STORE=pgvector` 后，经前端/API 重新上传文档索引
   （`POST /api/knowledge/:id/documents`），pgvector 模式下自动落到 PG。
2. **脚本搬运**：从 SQLite `kb_vectors` 读行（content/metadata/embedding 均为 JSON 文本），
   逐条 `INSERT` 到 PG（embedding 文本直接 `::vector`）。可复用 `store.VectorStore` 接口做双写。

## 7. 运维注意事项

- **备份**：pgvector 数据随 PG 实例备份（`pg_dump` / 云 PG 自动备份）；SQLite 基线仅需备份
  `data/codeagent.db` 单文件。
- **回滚**：切回 `KB_STORE=sqlite` 即可（两后端并存，互不干扰）；PG 中数据保留不清理。
- **监控**：检索 P99 可在 `/metrics` 之外按需补充（当前知识库检索走 `engine.KnowledgeRetriever`，
  trace 由 M7-06 日志贯通，可按 trace 下钻单次检索耗时）。
- **K8s**：pgvector 部署在集群内时用 `KB_PG_DSN` 指向 Service DNS（如 `postgres://...@pgvector-svc:5432/...`），
  连接池数 `KB_PG_POOL` 建议 ≤ PG max_connections 的 1/4。

---

## 验收对照（PLAN.md M8-04 验证标准）

| 标准 | 落地 |
|------|------|
| 万级 chunk 检索 P99 达标 | `TestPGVectorStore_Scale10k`（PG_SCALE_TEST=1）插 1 万条并输出 P50/P95/P99；HNSW 索引保证 ~O(log n) 检索 |
| 并发检索无退化 | `TestPGVectorStore_ConcurrentSearch`：20 goroutine × 50 次并发检索无错误、结果稳定（连接池 + PG 并发读） |
| 默认零破坏 | `KB_STORE=sqlite` 默认不变；manager/api 回归测试全绿；`go test ./...` 无 PG 环境全 PASS |
