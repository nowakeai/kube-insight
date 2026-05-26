package agent

import (
	"math"

	"github.com/dop251/goja"
)

const jsDataHelpersSource = `
const __kubeInsightDataHelpers = (() => {
  const keyOf = (item, key) => typeof key === "function" ? key(item) : item?.[key];
  const numberOf = (item, key) => {
    const value = typeof key === "function" ? key(item) : item?.[key];
    const number = Number(value);
    return Number.isFinite(number) ? number : 0;
  };
  const helpers = {
    groupBy(items, key) {
      return (items || []).reduce((out, item) => {
        const value = String(keyOf(item, key));
        (out[value] ||= []).push(item);
        return out;
      }, {});
    },
    countBy(items, key) {
      return (items || []).reduce((out, item) => {
        const value = String(keyOf(item, key));
        out[value] = (out[value] || 0) + 1;
        return out;
      }, {});
    },
    sumBy(items, key) {
      return (items || []).reduce((sum, item) => sum + numberOf(item, key), 0);
    },
    sortBy(items, key) {
      return [...(items || [])].sort((a, b) => {
        const av = keyOf(a, key);
        const bv = keyOf(b, key);
        return av < bv ? -1 : av > bv ? 1 : 0;
      });
    },
    uniqBy(items, key) {
      const seen = new Set();
      const out = [];
      for (const item of items || []) {
        const value = keyOf(item, key);
        if (seen.has(value)) continue;
        seen.add(value);
        out.push(item);
      }
      return out;
    },
    pick(item, keys) {
      const out = {};
      for (const key of keys || []) out[key] = item?.[key];
      return out;
    },
  };
  return Object.freeze(helpers);
})();
`

func installJSDataHelpers(vm *goja.Runtime) error {
	if _, err := vm.RunString(jsDataHelpersSource); err != nil {
		return err
	}
	helpers := vm.Get("__kubeInsightDataHelpers")
	if err := vm.Set("ki", helpers); err != nil {
		return err
	}
	return vm.Set("_", helpers)
}

func sanitizeJSExportForJSON(value any) any {
	switch typed := value.(type) {
	case float64:
		if math.IsNaN(typed) || math.IsInf(typed, 0) {
			return nil
		}
		return typed
	case float32:
		value := float64(typed)
		if math.IsNaN(value) || math.IsInf(value, 0) {
			return nil
		}
		return typed
	case []any:
		out := make([]any, len(typed))
		for i, item := range typed {
			out[i] = sanitizeJSExportForJSON(item)
		}
		return out
	case map[string]any:
		out := make(map[string]any, len(typed))
		for key, item := range typed {
			out[key] = sanitizeJSExportForJSON(item)
		}
		return out
	default:
		return value
	}
}
