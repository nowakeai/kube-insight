package agent

import "github.com/dop251/goja"

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
