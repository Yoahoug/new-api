package middleware

import (
	"slices"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/logger"
	pluginruntime "github.com/QuantumNous/new-api/pkg/jsplugin"
	"github.com/gin-gonic/gin"
)

// Request guards run after authentication and before channel selection, which
// is the only point on the relay path where the caller is known and nothing
// billable, routable, or upstream-facing has happened yet. A denial here
// therefore costs the gateway nothing and cannot be undone downstream.
const (
	// maxGuardBodyBytes bounds the request body handed to a guard. A guard is
	// an authorization decision, not a body transformer; a request that does
	// not fit is still guarded, with body reported as null.
	maxGuardBodyBytes = 256 << 10
	maxGuardHeaders   = 64
)

// guardCredentialHeaders never reach a guard. A guard decides whether a
// request may run; it has no need for the material that proves who sent it,
// and a denial must not depend on a plugin being able to read a secret.
var guardCredentialHeaders = map[string]struct{}{
	"authorization":       {},
	"proxy-authorization": {},
	"cookie":              {},
	"set-cookie":          {},
	"x-api-key":           {},
	"api-key":             {},
	"x-goog-api-key":      {},
	"x-auth-token":        {},
	"openai-api-key":      {},
	"anthropic-api-key":   {},
}

// guardCredentialHeaderSuffixes catch the vendor spellings an exact list
// always misses, so a new gateway header does not silently leak.
var guardCredentialHeaderSuffixes = []string{"-key", "-token", "-secret", "-password", "-signature"}

// PluginRequestGuard consults every installed request guard before the request
// reaches distribution. A denied request never selects a channel and never
// reserves quota.
func PluginRequestGuard() func(c *gin.Context) {
	return func(c *gin.Context) {
		generation := pluginruntime.DefaultRegistry.Generation()
		if generation == nil || !generation.HasRequestGuards() {
			c.Next()
			return
		}
		request := buildGuardRequest(c)
		outcome := generation.RunRequestGuards(c.Request.Context(), request)
		if !outcome.Allowed {
			abortWithGuardDecision(c, generation.Number, request, outcome)
			return
		}
		startedAt := time.Now()
		c.Next()
		generation.NotifyGuardCompletion(request, pluginruntime.GuardCompletion{
			RequestID:   request.RequestID,
			Outcome:     guardCompletionOutcome(c),
			StatusCode:  c.Writer.Status(),
			GuardKey:    outcome.Key,
			StartedAt:   startedAt,
			CompletedAt: time.Now(),
		})
	}
}

func buildGuardRequest(c *gin.Context) pluginruntime.GuardRequest {
	request := pluginruntime.GuardRequest{
		RequestID:  c.GetString(common.RequestIdKey),
		UnixNow:    common.GetTimestamp(),
		Method:     c.Request.Method,
		Path:       c.Request.URL.Path,
		ClientIP:   c.ClientIP(),
		UserID:     common.GetContextKeyInt(c, constant.ContextKeyUserId),
		TokenID:    common.GetContextKeyInt(c, constant.ContextKeyTokenId),
		TokenName:  c.GetString("token_name"),
		UserGroup:  common.GetContextKeyString(c, constant.ContextKeyUserGroup),
		UsingGroup: common.GetContextKeyString(c, constant.ContextKeyUsingGroup),
		Headers:    guardVisibleHeaders(c),
	}
	// getModelFromRequest is the same extractor distribution uses, including
	// the cache a task-plugin endpoint may already have filled, so a guard and
	// the relay never disagree about which model is being requested.
	if modelRequest, err := getModelFromRequest(c); err == nil && modelRequest != nil {
		request.Model = modelRequest.Model
	}
	request.Body, request.Stream = guardRequestBody(c)
	return request
}

func guardVisibleHeaders(c *gin.Context) map[string]string {
	headers := make(map[string]string, maxGuardHeaders)
	for name, values := range c.Request.Header {
		if len(headers) >= maxGuardHeaders {
			break
		}
		lower := strings.ToLower(name)
		if _, credential := guardCredentialHeaders[lower]; credential {
			continue
		}
		if isGuardCredentialHeader(lower) {
			continue
		}
		headers[lower] = strings.Join(values, ", ")
	}
	return headers
}

func isGuardCredentialHeader(lowerName string) bool {
	for _, suffix := range guardCredentialHeaderSuffixes {
		if strings.HasSuffix(lowerName, suffix) {
			return true
		}
	}
	return false
}

// guardRequestBody exposes a JSON body to guards and reports the stream flag
// from the same parse, so a guard never makes the host read the body twice.
func guardRequestBody(c *gin.Context) (any, bool) {
	if !strings.HasPrefix(c.Request.Header.Get("Content-Type"), "application/json") {
		return nil, false
	}
	storage, err := common.GetBodyStorage(c)
	if err != nil || storage.Size() > maxGuardBodyBytes {
		return nil, false
	}
	var body map[string]any
	if err = common.UnmarshalBodyReusable(c, &body); err != nil {
		return nil, false
	}
	stream, _ := body["stream"].(bool)
	return body, stream
}

func abortWithGuardDecision(c *gin.Context, generation uint64, request pluginruntime.GuardRequest, outcome pluginruntime.GuardOutcome) {
	for name, value := range outcome.Headers {
		c.Header(name, value)
	}
	detail := make([]string, 0, len(outcome.Metadata))
	for name, value := range outcome.Metadata {
		detail = append(detail, name+"="+value)
	}
	slices.Sort(detail)
	logger.LogWarn(c.Request.Context(), "plugin_guard event=denied generation=%d plugin=%q hook_error=%t status=%d code=%q user=%d model=%q path=%q detail=%q",
		generation, outcome.Key, outcome.Failed, outcome.Status, outcome.Code,
		request.UserID, request.Model, request.Path, strings.Join(detail, " "))
	c.JSON(outcome.Status, gin.H{
		"error": gin.H{
			"message": common.MessageWithRequestId(outcome.Message, c.GetString(common.RequestIdKey)),
			"type":    "new_api_error",
			"code":    outcome.Code,
		},
	})
	c.Abort()
}

func guardCompletionOutcome(c *gin.Context) string {
	if c.Request.Context().Err() != nil {
		return pluginruntime.GuardOutcomeCanceled
	}
	if modelRequestSucceeded(c) {
		return pluginruntime.GuardOutcomeSucceed
	}
	return pluginruntime.GuardOutcomeFailed
}
