package controller

import (
	"fmt"
	"sort"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/jsplugin"
	"github.com/QuantumNous/new-api/plugins"
	"github.com/gin-gonic/gin"
)

// requestGuardView is the administration snapshot of one installed guard. It
// pairs what the plugin declared with what an administrator has configured, so
// a client can render a form without interpreting the manifest itself.
type requestGuardView struct {
	Key         string                 `json:"key"`
	Name        string                 `json:"name"`
	Version     string                 `json:"version"`
	Icon        string                 `json:"icon"`
	HasIcon     bool                   `json:"hasIcon"`
	Description jsplugin.LocalizedText `json:"description"`
	Website     string                 `json:"website,omitempty"`
	Guard       *jsplugin.GuardMeta    `json:"guard"`
	Config      any                    `json:"config"`
}

// requestInterceptorView mirrors requestGuardView for the outbound
// interceptor chain.
type requestInterceptorView struct {
	Key         string                    `json:"key"`
	Name        string                    `json:"name"`
	Version     string                    `json:"version"`
	Icon        string                    `json:"icon"`
	HasIcon     bool                      `json:"hasIcon"`
	Description jsplugin.LocalizedText    `json:"description"`
	Website     string                    `json:"website,omitempty"`
	Interceptor *jsplugin.InterceptorMeta `json:"interceptor"`
	Config      any                       `json:"config"`
}

// ListRequestInterceptors returns every installed request interceptor in chain
// order with its stored configuration.
func ListRequestInterceptors(c *gin.Context) {
	generation := jsplugin.DefaultRegistry.Generation()
	if generation == nil {
		common.ApiSuccess(c, []requestInterceptorView{})
		return
	}
	stored := jsplugin.InterceptorConfigsSnapshot()
	interceptors := generation.OutboundInterceptors()
	views := make([]requestInterceptorView, 0, len(interceptors))
	for _, plugin := range interceptors {
		meta := plugin.Meta
		view := requestInterceptorView{
			Key:         meta.Key,
			Name:        meta.Name,
			Version:     meta.Version,
			Icon:        meta.Icon,
			Description: meta.Description,
			Website:     meta.Website,
			Interceptor: meta.Interceptor,
			Config:      stored[meta.Key],
		}
		if row, err := model.GetTaskPluginVersion(meta.Key, ""); err == nil {
			view.HasIcon = row.HasIcon()
		} else if _, _, hasIcon := plugins.Icon(meta.Key); hasIcon {
			view.HasIcon = true
		}
		views = append(views, view)
	}
	common.ApiSuccess(c, views)
}

// UpdateRequestInterceptorConfig replaces one interceptor's configuration with
// the same declared-field validation the guard surface uses.
func UpdateRequestInterceptorConfig(c *gin.Context) {
	key := c.Param("key")
	plugin, ok := jsplugin.DefaultRegistry.Get(key)
	if !ok {
		common.ApiError(c, fmt.Errorf("plugin %q is not installed", key))
		return
	}
	if !plugin.Meta.IsRequestInterceptor() {
		common.ApiError(c, fmt.Errorf("plugin %q is not a request interceptor", key))
		return
	}
	var body struct {
		Config any `json:"config"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		common.ApiError(c, err)
		return
	}
	config := body.Config
	if config == nil {
		config = map[string]any{}
	}
	object, isObject := config.(map[string]any)
	if !isObject {
		common.ApiError(c, fmt.Errorf("interceptor config must be a JSON object"))
		return
	}
	if err := validateGuardConfig(plugin.Meta.Interceptor.ConfigFields, object); err != nil {
		common.ApiError(c, err)
		return
	}
	stored := jsplugin.InterceptorConfigsSnapshot()
	stored[key] = object
	encoded, err := common.Marshal(stored)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if err = model.UpdateOption(jsplugin.TaskPluginInterceptorConfigsKey, string(encoded)); err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, object)
}

// ListRequestGuards returns every installed request guard in chain order.
func ListRequestGuards(c *gin.Context) {
	generation := jsplugin.DefaultRegistry.Generation()
	if generation == nil {
		common.ApiSuccess(c, []requestGuardView{})
		return
	}
	stored := jsplugin.GuardConfigsSnapshot()
	guards := generation.RequestGuards()
	views := make([]requestGuardView, 0, len(guards))
	for _, plugin := range guards {
		meta := plugin.Meta
		view := requestGuardView{
			Key:         meta.Key,
			Name:        meta.Name,
			Version:     meta.Version,
			Icon:        meta.Icon,
			Description: meta.Description,
			Website:     meta.Website,
			Guard:       meta.Guard,
			Config:      stored[meta.Key],
		}
		if row, err := model.GetTaskPluginVersion(meta.Key, ""); err == nil {
			view.HasIcon = row.HasIcon()
		} else if _, _, hasIcon := plugins.Icon(meta.Key); hasIcon {
			view.HasIcon = true
		}
		views = append(views, view)
	}
	common.ApiSuccess(c, views)
}

// UpdateRequestGuardConfig replaces one guard's configuration. The value is
// validated against the fields the plugin declared, so a typo is rejected
// instead of silently leaving a rule the administrator believed was active.
func UpdateRequestGuardConfig(c *gin.Context) {
	key := c.Param("key")
	plugin, ok := jsplugin.DefaultRegistry.Get(key)
	if !ok {
		common.ApiError(c, fmt.Errorf("plugin %q is not installed", key))
		return
	}
	if !plugin.Meta.IsRequestGuard() {
		common.ApiError(c, fmt.Errorf("plugin %q is not a request guard", key))
		return
	}
	var body struct {
		Config any `json:"config"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		common.ApiError(c, err)
		return
	}
	config := body.Config
	if config == nil {
		config = map[string]any{}
	}
	object, isObject := config.(map[string]any)
	if !isObject {
		common.ApiError(c, fmt.Errorf("guard config must be a JSON object"))
		return
	}
	if err := validateGuardConfig(plugin.Meta.Guard.ConfigFields, object); err != nil {
		common.ApiError(c, err)
		return
	}
	stored := jsplugin.GuardConfigsSnapshot()
	stored[key] = object
	encoded, err := common.Marshal(stored)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if err = model.UpdateOption(jsplugin.TaskPluginGuardConfigsKey, string(encoded)); err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, object)
}

func validateGuardConfig(fields []jsplugin.GuardConfigField, config map[string]any) error {
	declared := make(map[string]jsplugin.GuardConfigField, len(fields))
	for _, field := range fields {
		declared[field.Name] = field
	}
	for name := range config {
		if _, ok := declared[name]; !ok {
			return fmt.Errorf("guard config field %q is not declared by the plugin", name)
		}
	}
	names := make([]string, 0, len(declared))
	for name := range declared {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		value, present := config[name]
		if !present {
			continue
		}
		if err := checkGuardConfigValue(declared[name], value); err != nil {
			return err
		}
	}
	return nil
}

func checkGuardConfigValue(field jsplugin.GuardConfigField, value any) error {
	if jsplugin.IsGuardConfigValue(field, value) {
		return nil
	}
	if field.Type == "enum" {
		return fmt.Errorf("guard config field %q must be one of its declared enumValues", field.Name)
	}
	return fmt.Errorf("guard config field %q must be a %s", field.Name, field.Type)
}
