package handler

import "github.com/gin-gonic/gin"

func geminiCachedContentConcurrencyError(c *gin.Context, err error, slot string) {
	status, _, _, message := concurrencyErrorResponse(err, slot)
	googleError(c, status, message)
}
