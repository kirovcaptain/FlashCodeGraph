package typescript

// jsGlobalObjects contains JavaScript/TypeScript built-in global objects.
// When a call's receiver matches one of these, it should not fall through
// to project-level matching (name_unique, same_file, etc.) because these
// are external runtime objects, not project symbols.
var jsGlobalObjects = map[string]bool{
	// Console / IO
	"console": true,

	// Math / JSON
	"Math": true, "JSON": true,

	// Built-in constructors used as namespaces
	"Object": true, "Array": true, "String": true, "Number": true,
	"Boolean": true, "Date": true, "RegExp": true, "Error": true,
	"Map": true, "Set": true, "WeakMap": true, "WeakSet": true,
	"Promise": true, "Symbol": true, "BigInt": true,
	"ArrayBuffer": true, "DataView": true,
	"Int8Array": true, "Uint8Array": true, "Float32Array": true, "Float64Array": true,

	// Global functions (used as receivers in chained calls)
	"Reflect": true, "Proxy": true, "Intl": true,

	// Node.js globals
	"process": true, "Buffer": true,

	// Browser globals
	"window": true, "document": true, "navigator": true, "localStorage": true,
	"sessionStorage": true, "fetch": true, "XMLHttpRequest": true,
}

// IsGlobalObject returns true if the name is a JS/TS built-in global object.
func IsGlobalObject(name string) bool {
	return jsGlobalObjects[name]
}
