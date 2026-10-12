//go:build windows

package runtime

import (
	"strings"

	"github.com/eslzzyl/krkrgo/internal/tjs"
	"golang.org/x/sys/windows/registry"
)

// readSystemRegistryValue accepts the full hive\\key\\value form documented
// by System.readRegValue and returns only string or numeric values.
func readSystemRegistryValue(spec string) (tjs.Value, bool) {
	separator := strings.IndexByte(spec, '\\')
	if separator <= 0 || separator == len(spec)-1 {
		return tjs.VoidValue, false
	}
	rootName, remainder := spec[:separator], spec[separator+1:]
	valueSeparator := strings.LastIndexByte(remainder, '\\')
	if valueSeparator < 0 || valueSeparator == len(remainder)-1 {
		return tjs.VoidValue, false
	}
	keyPath, valueName := remainder[:valueSeparator], remainder[valueSeparator+1:]

	var root registry.Key
	switch strings.ToUpper(rootName) {
	case "HKEY_CLASSES_ROOT":
		root = registry.CLASSES_ROOT
	case "HKEY_CURRENT_CONFIG":
		root = registry.CURRENT_CONFIG
	case "HKEY_CURRENT_USER":
		root = registry.CURRENT_USER
	case "HKEY_LOCAL_MACHINE":
		root = registry.LOCAL_MACHINE
	case "HKEY_USERS":
		root = registry.USERS
	case "HKEY_PERFORMANCE_DATA":
		root = registry.PERFORMANCE_DATA
	case "HKEY_DYN_DATA":
		root = registry.Key(0x80000006)
	default:
		return tjs.VoidValue, false
	}
	key, err := registry.OpenKey(root, keyPath, registry.QUERY_VALUE)
	if err != nil {
		return tjs.VoidValue, false
	}
	defer key.Close()
	if value, _, err := key.GetStringValue(valueName); err == nil {
		return tjs.StringValue(value), true
	}
	if value, _, err := key.GetIntegerValue(valueName); err == nil {
		return tjs.IntegerValue(int64(value)), true
	}
	return tjs.VoidValue, false
}
