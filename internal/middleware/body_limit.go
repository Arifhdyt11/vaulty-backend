package middleware

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

// MultipartLimit membatasi ukuran body upload. Huma sudah membatasi body JSON (1 MB),
// tetapi tidak untuk multipart.
func MultipartLimit(maxBytes int64) gin.HandlerFunc {
	return func(c *gin.Context) {
		if strings.HasPrefix(c.GetHeader("Content-Type"), "multipart/") {
			c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxBytes)
		}
		c.Next()
	}
}
