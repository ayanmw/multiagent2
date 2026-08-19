package api

import (
	"net/http"

	"github.com/ayanmw/multiagent2/server/internal/metrics"
	"github.com/ayanmw/multiagent2/server/internal/repo"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// MonitoringOverviewHandler handles GET /api/monitoring/overview (M3-09).
//
// 返回当前进程内的可观测性指标聚合快照（metrics.Summary），供前端「运行监控」
// 概览卡片渲染：LLM 调用数 / 失败数、工具调用数 / 失败数、token 用量、自主 Loop
// 运行/失败、预算耗尽，以及 M7-05 新增的实时 gauge（Active Loops / 检查点堆积）。
// 指标来自 OpenTelemetry MeterProvider 的进程内原子累加器，无需查询数据库，
// 因此开销极低、可高频轮询。需登录（与 /api/usage 同级保护）。
func MonitoringOverviewHandler(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		if _, ok := currentUserID(c); !ok {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "not authenticated"})
			return
		}
		// M7-05：实时回填「检查点堆积」gauge——按 DB 查待审批检查点数，使 Grafana 看板
		// 与前端概览读到的数字一致（该 gauge 为快照型，非累计计数器）。
		if db != nil {
			metrics.SetPendingCheckpoints(repo.CountPendingCheckpoints(db))
		}
		c.JSON(http.StatusOK, metrics.Summary())
	}
}
