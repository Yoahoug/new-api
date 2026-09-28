package plugins

import (
	"embed"
	"encoding/base64"
	"fmt"
	"io/fs"
	"strings"

	"github.com/QuantumNous/new-api/pkg/jsplugin"
)

//go:embed tasks
var taskPlugins embed.FS

//go:embed guards
var guardPlugins embed.FS

func init() {
	entries, err := fs.ReadDir(taskPlugins, "tasks")
	if err != nil {
		panic(fmt.Sprintf("read embedded task plugins: %v", err))
	}
	for _, entry := range entries {
		if (!entry.IsDir() && entry.Name() != ".DS_Store") || strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		key := entry.Name()
		source, sourceErr := Source(key)
		if sourceErr != nil {
			panic(fmt.Sprintf("read embedded task plugin %s: %v", key, sourceErr))
		}
		if _, registerErr := jsplugin.DefaultRegistry.RegisterFactory(source, jsplugin.Options{Key: key}); registerErr != nil {
			panic(fmt.Sprintf("register embedded task plugin %s: %v", key, registerErr))
		}
		if mediaType, data, ok := Icon(key); ok {
			if iconErr := jsplugin.ValidateIconImage(mediaType, data); iconErr != nil {
				panic(fmt.Sprintf("embedded task plugin %s icon: %v", key, iconErr))
			}
		}
	}
	registerEmbeddedKind(guardPlugins, "guards", GuardSource, "request guard")
}

// registerEmbeddedKind compiles and registers one directory of factory
// plugins. Registration panics on failure: a broken built-in must stop the
// build, not surface as a runtime routing error.
func registerEmbeddedKind(embedded embed.FS, dir string, sourceOf func(key string) (string, error), kind string) {
	entries, err := fs.ReadDir(embedded, dir)
	if err != nil {
		panic(fmt.Sprintf("read embedded %ss: %v", kind, err))
	}
	for _, entry := range entries {
		if (!entry.IsDir() && entry.Name() != ".DS_Store") || strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		key := entry.Name()
		source, sourceErr := sourceOf(key)
		if sourceErr != nil {
			panic(fmt.Sprintf("read embedded %s %s: %v", kind, key, sourceErr))
		}
		if _, registerErr := jsplugin.DefaultRegistry.RegisterFactory(source, jsplugin.Options{Key: key}); registerErr != nil {
			panic(fmt.Sprintf("register embedded %s %s: %v", kind, key, registerErr))
		}
	}
}

// Source returns the embedded factory source for a task plugin key.
func Source(key string) (string, error) {
	return readFactoryPlugin(taskPlugins, "tasks", key)
}

// GuardSource returns the embedded factory source for a request guard key.
func GuardSource(key string) (string, error) {
	return readFactoryPlugin(guardPlugins, "guards", key)
}

func readFactoryPlugin(embedded embed.FS, dir, key string) (string, error) {
	source, err := embedded.ReadFile(dir + "/" + key + "/plugin.js")
	if err != nil {
		return "", err
	}
	return string(source), nil
}

// Icon returns the embedded sidecar logo for a factory plugin, if the plugin
// directory ships an icon.svg or icon.png next to plugin.js.
func Icon(key string) (mediaType string, data []byte, ok bool) {
	if data, err := taskPlugins.ReadFile("tasks/" + key + "/icon.svg"); err == nil {
		return "image/svg+xml", data, true
	}
	if data, err := taskPlugins.ReadFile("tasks/" + key + "/icon.png"); err == nil {
		return "image/png", data, true
	}
	return "", nil, false
}

// IconDataURI returns the embedded factory logo in the same data URI form the
// task_plugins.icon column stores, so factory and override logos share one
// read path.
func IconDataURI(key string) string {
	mediaType, data, ok := Icon(key)
	if !ok {
		return ""
	}
	return "data:" + mediaType + ";base64," + base64.StdEncoding.EncodeToString(data)
}
