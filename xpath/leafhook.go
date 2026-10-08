package xpath

import "github.com/knroy/go-xml/internal/xpathleaf"

func init() {
	xpathleaf.Mark = func(fn any) { fn.(*Function).leaf = true }
}
