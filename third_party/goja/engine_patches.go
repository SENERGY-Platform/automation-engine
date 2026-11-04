package goja

// EnginePatches names the patches of the automation engine in this copy of goja
// (third_party/goja-patches). The engine refers to it, so that it does not build with goja
// unpatched. Each patch adds its name in a file of its own.
func EnginePatches() []string {
	return enginePatches
}

var enginePatches = []string{"engine-patches"}
