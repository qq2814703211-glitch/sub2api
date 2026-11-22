package handler

import (
	"context"
	"github.com/Wei-Shaw/sub2api/internal/pkg/httputil"
	"github.com/Wei-Shaw/sub2api/internal/pkg/requestmodel"
	middleware "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"net/http"
	"strings"
)

// PrepareAPIKeyRoutes runs before protocol dispatch and composite rewriting.
func (h *GatewayHandler) PrepareAPIKeyRoutes(resolver *service.CompositeRouteResolver) gin.HandlerFunc {
	return func(c *gin.Context) {
		key, ok := middleware.GetAPIKeyFromContext(c)
		// All gateway protocols pass here after authentication and before route
		// selection, queues and reservations. Responses WS pins its first frame.
		if ok && h != nil && h.gatewayService != nil && !(c.Request.Method == http.MethodGet && strings.HasSuffix(c.FullPath(), "/responses")) {
			ctx := h.gatewayService.WithRequestCatalog(c.Request.Context())
			ctx = service.RequestCatalogFromContext(ctx).WithPricingGroup(key.Group).Context(ctx)
			c.Request = c.Request.WithContext(ctx)
		}
		if !ok || !key.HasMultipleRoutes() {
			c.Next()
			return
		}
		path := c.Request.URL.Path
		endpoint := service.CompositeRouteEndpointAny
		switch {
		case strings.Contains(path, "/messages"):
			endpoint = service.CompositeRouteEndpointMessages
		case strings.Contains(path, "/responses"):
			endpoint = service.CompositeRouteEndpointResponses
		case strings.Contains(path, "/chat/completions"):
			endpoint = service.CompositeRouteEndpointChatCompletions
		case strings.Contains(path, "/v1beta"):
			endpoint = service.CompositeRouteEndpointGemini
		case strings.Contains(path, "/images/"):
			endpoint = service.CompositeRouteEndpointImages
		case strings.Contains(path, "/embeddings"):
			endpoint = service.CompositeRouteEndpointEmbeddings
		}
		ctx := service.ContextWithAPIKeyRouteResolver(c.Request.Context(), resolver, endpoint)
		c.Request = c.Request.WithContext(ctx)
		// WS chooses from the first frame; resource GET/DELETE keep server ownership.
		if c.Request.Method == http.MethodGet || c.Request.Method == http.MethodDelete {
			c.Next()
			return
		}
		body, err := httputil.ReadRequestBodyWithPrealloc(c.Request)
		if err != nil {
			c.AbortWithStatusJSON(400, gin.H{"error": gin.H{"message": err.Error()}})
			return
		}
		requestmodel.ResetRequestBody(c.Request, body)
		model := requestmodel.FromBodyForRoute(c.FullPath(), c.GetHeader("Content-Type"), body)
		if action := strings.TrimPrefix(c.Param("modelAction"), "/"); action != "" {
			model = strings.Split(action, ":")[0]
		}
		if model == "" {
			c.Next()
			return
		}
		ctx = service.ContextWithAPIKeyRouteModels(ctx, requestmodel.FromBodyCandidates(c.FullPath(), c.GetHeader("Content-Type"), body))
		if service.IsExplicitImageGenerationIntent(path, model, body) || strings.Contains(path, "/images/") {
			ctx = service.WithOpenAIImageGenerationIntent(ctx)
		}
		platform := ""
		switch {
		case strings.Contains(path, "/v1beta"):
			platform = service.PlatformGemini
		case strings.Contains(path, "/images/") || strings.Contains(path, "/embeddings"):
			platform = service.PlatformOpenAI
		}
		if h.billingCacheService != nil {
			ctx = service.ContextWithAPIKeyRouteEligibility(ctx, func(routeCtx context.Context, candidate *service.APIKey) error {
				sub, err := h.gatewayService.ResolveAPIKeyRouteSubscription(routeCtx, candidate, nil)
				if err != nil {
					return err
				}
				return h.billingCacheService.CheckAPIKeyRouteEligibility(routeCtx, candidate, sub)
			})
		}
		routed, err := h.gatewayService.PrepareAPIKeyRoute(ctx, key, model, platform)
		if err != nil {
			c.AbortWithStatusJSON(403, gin.H{"error": gin.H{"type": "permission_error", "message": err.Error()}})
			return
		}
		c.Set(string(middleware.ContextKeyAPIKey), routed)
		c.Request = c.Request.WithContext(service.ContextWithAPIKeyRoute(ctx, routed))
		c.Next()
	}
}
