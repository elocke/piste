package cli

// cobraBuiltins are handled by cobra/fang itself.
var cobraBuiltins = map[string]bool{
	"help": true, "completion": true, "man": true,
	"__complete": true, "__completeNoDesc": true,
}
