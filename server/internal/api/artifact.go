package api

import (
	"mime"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"time"

	"github.com/ayanmw/multiagent2/server/internal/artifact"
	"github.com/ayanmw/multiagent2/server/internal/repo"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// artifactMaxInlineBytes 是「查看」接口内联返回内容的上限（256 KiB）。
// 超出部分被截断（truncated=true），完整内容请走 ?download=1 下载，
// 避免单个大产物撑爆前端内存与 JSON 序列化开销。
const artifactMaxInlineBytes = 256 * 1024

// artifactEntryView 是 artifact 列表项的对外视图（M3-06）。
// is_state 标识它是否为 M1-16 的三个核心工作状态文件，前端据此
// 与「运行态面板」呼应：面板看三核心文件，浏览器看全部产物。
type artifactEntryView struct {
	Name       string `json:"name"`
	Size       int64  `json:"size"`
	ModifiedAt string `json:"modified_at"`
	IsState    bool   `json:"is_state"`
}

// artifactListView 是 GET /api/sessions/:id/artifacts 的响应体。
// enabled=false 表示状态外置未启用（STATE_ENABLED=false 或未配置存储），
// 此时 artifacts 恒为空数组，前端应提示「未启用」而非「无产物」。
type artifactListView struct {
	SessionKey string              `json:"session_key"`
	Enabled    bool                `json:"enabled"`
	Total      int                 `json:"total"`
	Artifacts  []artifactEntryView `json:"artifacts"`
}

// artifactContentView 是 GET /api/sessions/:id/artifacts/:name 的响应体（查看模式）。
type artifactContentView struct {
	SessionKey string `json:"session_key"`
	Name       string `json:"name"`
	Size       int64  `json:"size"`
	IsState    bool   `json:"is_state"`
	Binary     bool   `json:"binary"`
	Truncated  bool   `json:"truncated"`
	Content    string `json:"content"`
}

// artifactScopeKey 计算某会话的 artifact 作用域键，必须与 StateEnforcer
// 写入时使用的 goalScope（"sess:<sessionID>"）保持一致。
func artifactScopeKey(sessionKey string) string { return "sess:" + sessionKey }

// resolveArtifactSession 完成「认证 + 会话归属校验 + 路径参数校验」三件事。
// 校验失败时已写好响应，返回 ok=false 由调用方直接 return。
func resolveArtifactSession(c *gin.Context, db *gorm.DB) (sessionKey string, ok bool) {
	uid, authed := currentUserID(c)
	if !authed {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "not authenticated"})
		return "", false
	}
	key := c.Param("id")
	if key == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid session id"})
		return "", false
	}
	// owner 隔离：只能浏览自己会话下的产物（跨用户一律 404，不泄漏存在性）。
	if _, err := repo.GetSessionByKey(db, uid, key); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "session not found"})
		return "", false
	}
	return key, true
}

// listArtifactEntries 优先走 EntryLister 拿到带体积/时间的元信息；
// 后端未实现该扩展时回退为 List + Read 自行统计（保证任何 Store 都能用）。
func listArtifactEntries(store artifact.Store, scope string) ([]artifact.Entry, error) {
	if lister, okLister := store.(artifact.EntryLister); okLister {
		return lister.ListEntries(scope)
	}
	names, err := store.List(scope)
	if err != nil {
		return nil, err
	}
	entries := make([]artifact.Entry, 0, len(names))
	for _, n := range names {
		content, found, rerr := store.Read(scope, n)
		if rerr != nil || !found {
			continue
		}
		entries = append(entries, artifact.Entry{Name: n, Size: int64(len(content))})
	}
	artifact.SortEntries(entries)
	return entries, nil
}

// ListSessionArtifactsHandler handles GET /api/sessions/:id/artifacts (M3-06).
//
// 列出某会话作用域下的全部 artifact（PLAN/PROGRESS/LEARNINGS 以及 Agent 写下的
// 报告 / diff / 构建产物等），核心状态文件排在最前。仅返回元信息，内容由
// GET /api/sessions/:id/artifacts/:name 按需读取，避免一次性拉全量内容。
func ListSessionArtifactsHandler(db *gorm.DB, store artifact.Store, enableState bool) gin.HandlerFunc {
	return func(c *gin.Context) {
		key, ok := resolveArtifactSession(c, db)
		if !ok {
			return
		}
		resp := artifactListView{
			SessionKey: key,
			Enabled:    enableState && store != nil,
			Artifacts:  []artifactEntryView{},
		}
		if !resp.Enabled {
			c.JSON(http.StatusOK, resp)
			return
		}
		entries, err := listArtifactEntries(store, artifactScopeKey(key))
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list artifacts"})
			return
		}
		for _, e := range entries {
			view := artifactEntryView{
				Name:    e.Name,
				Size:    e.Size,
				IsState: artifact.IsStateArtifact(e.Name),
			}
			if !e.ModTime.IsZero() {
				view.ModifiedAt = e.ModTime.Format(time.RFC3339)
			}
			resp.Artifacts = append(resp.Artifacts, view)
		}
		resp.Total = len(resp.Artifacts)
		c.JSON(http.StatusOK, resp)
	}
}

// GetSessionArtifactHandler handles GET /api/sessions/:id/artifacts/:name (M3-06).
//
// 默认返回 JSON（查看模式，超过 artifactMaxInlineBytes 截断并置 truncated=true）；
// 带 ?download=1 时以附件形式返回原始字节（下载模式）。
// 非法文件名（含路径分隔符 / 越界字符）返回 400，不存在返回 404。
func GetSessionArtifactHandler(db *gorm.DB, store artifact.Store, enableState bool) gin.HandlerFunc {
	return func(c *gin.Context) {
		key, ok := resolveArtifactSession(c, db)
		if !ok {
			return
		}
		name := strings.TrimSpace(c.Param("name"))
		if err := artifact.ValidateName(name); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "非法的 artifact 文件名"})
			return
		}
		if !enableState || store == nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "artifact not found"})
			return
		}
		content, found, err := store.Read(artifactScopeKey(key), name)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to read artifact"})
			return
		}
		if !found {
			c.JSON(http.StatusNotFound, gin.H{"error": "artifact not found"})
			return
		}

		if isTruthyQuery(c.Query("download")) {
			writeArtifactDownload(c, name, content)
			return
		}

		view := artifactContentView{
			SessionKey: key,
			Name:       name,
			Size:       int64(len(content)),
			IsState:    artifact.IsStateArtifact(name),
			Binary:     strings.ContainsRune(content, 0),
		}
		switch {
		case view.Binary:
			// 二进制产物不内联（会破坏 JSON 与前端渲染），引导走下载。
			view.Content = ""
		case len(content) > artifactMaxInlineBytes:
			view.Truncated = true
			view.Content = content[:artifactMaxInlineBytes]
		default:
			view.Content = content
		}
		c.JSON(http.StatusOK, view)
	}
}

// writeArtifactDownload 以附件形式回写产物原始内容。
// filename* 用 RFC 5987 编码兜底非 ASCII 文件名（当前命名规则已限制为
// ASCII，此处仍保留以防后续放宽命名约束）。
func writeArtifactDownload(c *gin.Context, name, content string) {
	ctype := mime.TypeByExtension(filepath.Ext(name))
	if ctype == "" {
		ctype = "text/plain; charset=utf-8"
	}
	c.Header("Content-Disposition",
		"attachment; filename=\""+name+"\"; filename*=UTF-8''"+url.PathEscape(name))
	c.Header("X-Content-Type-Options", "nosniff")
	c.Data(http.StatusOK, ctype, []byte(content))
}

// isTruthyQuery 把常见的「真」取值（1/true/yes）归一化，便于 ?download=1 与
// ?download=true 都能生效。
func isTruthyQuery(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "yes":
		return true
	default:
		return false
	}
}
