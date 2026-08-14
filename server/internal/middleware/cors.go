package middleware

import (
	"github.com/gin-gonic/gin"
)

// CORSOptions configures the CORS middleware (MX-07).
type CORSOptions struct {
	// AllowedOrigins is the explicit list of origins permitted to make
	// cross-origin requests. An origin not in this list is rejected (no
	// Access-Control-Allow-Origin header is set), which is the secure default
	// — browsers then block the response. The special value "*" allows any
	// origin (use only for public, non-credentialed APIs).
	AllowedOrigins []string
	// AllowCredentials enables credentials (cookies, Authorization header) in
	// cross-origin requests. Per the CORS spec this requires a concrete origin
	// (not "*"), so it is only emitted when an explicit origin matches.
	AllowCredentials bool
}

// CORS returns a Gin middleware enforcing an allowlist of frontend origins
// (MX-07). It handles preflight (OPTIONS) requests by responding 204 with the
// permitted methods/headers and short-circuits before routing/auth, so a
// browser preflight never reaches a protected handler.
//
// Security posture: only origins in AllowedOrigins receive an
// Access-Control-Allow-Origin header. There is no silent fallback to the
// request Origin, preventing naive reflection-based CSRF-style cross-origin
// access.
func CORS(opts CORSOptions) gin.HandlerFunc {
	allowAll := false
	exact := make(map[string]struct{}, len(opts.AllowedOrigins))
	for _, o := range opts.AllowedOrigins {
		if o == "*" {
			allowAll = true
		}
		exact[o] = struct{}{}
	}
	return func(c *gin.Context) {
		origin := c.GetHeader("Origin")
		if origin != "" {
			switch {
			case allowAll:
				// Wildcard: cannot be combined with credentials.
				c.Header("Access-Control-Allow-Origin", "*")
			case opts.AllowCredentials:
				if _, ok := exact[origin]; ok {
					c.Header("Access-Control-Allow-Origin", origin)
					c.Header("Vary", "Origin")
					c.Header("Access-Control-Allow-Credentials", "true")
				}
			default:
				if _, ok := exact[origin]; ok {
					c.Header("Access-Control-Allow-Origin", origin)
					c.Header("Vary", "Origin")
				}
			}
			c.Header("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, PATCH, OPTIONS")
			c.Header("Access-Control-Allow-Headers", "Authorization, X-API-Key, Content-Type, Accept, Origin")
			c.Header("Access-Control-Max-Age", "600")
		}

		// Short-circuit browser preflight only when it carries an Origin
		// (real preflight always does). Non-CORS OPTIONS fall through to Gin's
		// normal handling.
		if c.Request.Method == "OPTIONS" && origin != "" {
			c.AbortWithStatus(204)
			return
		}
		c.Next()
	}
}
