//go:build darwin

package scan

import (
	"path/filepath"
	"strings"
)

var packageKinds = map[string]string{
	".app":           "application",
	".appex":         "application-extension",
	".bundle":        "bundle",
	".framework":     "framework",
	".imovielibrary": "imovie-library",
	".musiclibrary":  "music-library",
	".photoslibrary": "photos-library",
	".photolibrary":  "photo-library",
	".plugin":        "plugin",
	".pvm":           "parallels-vm",
	".simruntime":    "simulator-runtime",
	".sparsebundle":  "sparse-bundle",
	".sparseimage":   "sparse-image",
	".utm":           "utm-vm",
	".vmwarevm":      "vmware-vm",
	".xcarchive":     "xcode-archive",
	".xpc":           "xpc-service",
}

func annotateItem(root string, item *Item) {
	name := filepath.Base(item.Path)
	if kind, ok := packageKinds[strings.ToLower(filepath.Ext(name))]; ok {
		item.PackageKind = kind
	}

	switch libraryCollection(root) {
	case "Containers":
		item.Role = "app-sandbox-container"
		item.Producer = identifierProducer(name, "high")
	case "Group Containers":
		item.Role = "app-group-container"
		item.Producer = identifierProducer(name, "high")
	case "Application Support":
		item.Role = "application-support"
		item.Producer = identifierProducer(name, "medium")
	case "Caches":
		item.Role = "application-cache"
		item.Producer = identifierProducer(name, "medium")
	case "Logs":
		item.Role = "application-log"
		item.Producer = identifierProducer(name, "medium")
	case "Metadata":
		item.Role = "search-metadata"
	case "CloudStorage":
		item.Role = "cloud-storage-provider"
		item.Producer = identifierProducer(name, "medium")
	}
}

func libraryCollection(path string) string {
	clean := filepath.Clean(path)
	parent := filepath.Dir(clean)
	if filepath.Base(parent) != "Library" {
		return ""
	}
	return filepath.Base(clean)
}

func identifierProducer(identifier, confidence string) *Producer {
	if identifier == "" || strings.HasPrefix(identifier, ".") {
		return nil
	}
	if confidence == "high" && !strings.Contains(identifier, ".") {
		confidence = "low"
	}
	return &Producer{
		Identifier: identifier,
		Confidence: confidence,
	}
}

func (item *Item) finalizeProperties() {
	if item.CloneFiles > 0 {
		item.Properties = append(item.Properties, "apfs-clone")
	}
	if item.SharedBlockFiles > 0 {
		item.Properties = append(item.Properties, "may-share-blocks")
	}
	if item.SparseFiles > 0 {
		item.Properties = append(item.Properties, "sparse")
	}
	if item.PurgeableItems > 0 {
		item.Properties = append(item.Properties, "purgeable")
	}
	if item.SyncRoots > 0 {
		item.Properties = append(item.Properties, "sync-root")
	}
	if item.SyntheticItems > 0 {
		item.Properties = append(item.Properties, "synthetic")
	}
}
