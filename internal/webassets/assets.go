package webassets

import "embed"
import "io/fs"

//go:embed all:dist
var assets embed.FS

func FS() fs.FS {
	sub, err := fs.Sub(assets, "dist")
	if err != nil {
		panic(err)
	}
	return sub
}
