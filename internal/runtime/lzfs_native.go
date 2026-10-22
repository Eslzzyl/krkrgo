package runtime

import (
	"fmt"
	"strings"

	"github.com/eslzzyl/krkrgo/internal/assets"
)

const lzfsSchemePrefix = "lzfs://"

func lzfsStorageSource(name string) (string, bool) {
	if len(name) < len(lzfsSchemePrefix) || !strings.EqualFold(name[:len(lzfsSchemePrefix)], lzfsSchemePrefix) {
		return "", false
	}
	return name[len(lzfsSchemePrefix):], true
}

func (s *Services) lzfsPluginLinked() bool {
	if s == nil {
		return false
	}
	for _, plugin := range s.Plugins {
		key := pluginLinkKey(plugin)
		if key == "lzfs.dll" || strings.HasSuffix(key, "/lzfs.dll") {
			return true
		}
	}
	return false
}

func (s *Services) lzfsExists(source string, search bool) bool {
	if !s.lzfsPluginLinked() || s.Storage == nil || source == "" {
		return false
	}
	if s.SaveDir != "" {
		save := s.storagesOverlaySave()
		if exists, err := save.Exists(source); err == nil && exists {
			return true
		}
	}
	if search {
		_, err := s.Storage.Resolve(source)
		return err == nil
	}
	exists, err := s.Storage.ExistsNoSearch(source)
	return err == nil && exists
}

func lzfsUnavailable(name string) error {
	return fmt.Errorf("%w: %s", assets.ErrUnsupportedStorageMedia, name)
}
