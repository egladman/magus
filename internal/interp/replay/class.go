package replay

import (
	"strings"

)

// liveModules name state in the process that made them: a handle table, an open file
// or socket, a cursor. A recorded answer cannot rebuild that state. Capitalized entries
// are type prefixes natives report, such as File.read.
var liveModules = map[string]bool{
	"io": true, "iocore": true, "ffi": true, "buffer": true, "pipe": true,
	"screen": true, "notify": true, "progress": true, "termkit": true, "layout": true,
	"pick": true, "recorder": true,
	"Buffer": true, "File": true, "Socket": true, "TcpServer": true, "Boxed": true,
}

// answerModules return data, which a record can answer.
var answerModules = map[string]bool{
	"archive": true, "base64": true, "buzz": true, "charm": true, "crypto": true, "cryptocore": true,
	"csv": true, "diff": true, "env": true, "flags": true, "fmt": true, "fs": true, "hex": true,
	"html": true, "http": true, "image": true, "ini": true, "json": true, "log": true,
	"markdown": true, "math": true, "merge": true, "net": true, "os": true, "path": true,
	"platform": true, "proc": true, "semver": true, "sort": true, "splice": true, "strings": true,
	"template": true, "term": true, "time": true, "toml": true, "url": true, "uuid": true,
	"vcs": true, "xml": true, "yaml": true,
	"std": true, "gc": true, "debug": true, "serialize": true, "serializecore": true, "errors": true,
	"assert": true, "assertcore": true, "suite": true, "testing": true, "test": true,
	"magus": true, "spell": true, "ctx": true, "mergequeue": true,
}

// liveMethods are natives inside an answer module that still name process state. They
// decide before their module does.
var liveMethods = map[string]bool{
	"fs.watch": true, "http.server": true, "image.open": true, "term.spin": true,
	"magus.service.acquire": true, "magus.service.release": true, "magus.lock": true,
}

// NativeClass is whether the recorded result of the native name can answer a replayed call.
// name is the one the call interceptor reports, such as "fs.readFile". ok is false when
// neither the method nor its module is classed.
func NativeClass(name string) (Class, bool) {
	if liveMethods[name] {
		return classLive, true
	}
	module, _, _ := strings.Cut(name, ".")
	switch {
	case liveModules[module]:
		return classLive, true
	case answerModules[module]:
		return classAnswer, true
	}
	return "", false
}

// repeatable natives change nothing outside the process. A module name covers each of
// its methods.
var repeatable = map[string]bool{
	"math": true, "path": true, "strings": true, "platform": true, "semver": true, "sort": true,
	"url": true, "hex": true, "base64": true, "json": true, "yaml": true, "toml": true, "csv": true,
	"xml": true, "ini": true, "time": true, "uuid": true,

	"fs.basename": true, "fs.dirname": true, "fs.ext": true, "fs.join": true, "fs.currentDirectory": true,
	"fs.diskUsage": true, "fs.exists": true, "fs.glob": true, "fs.isDir": true, "fs.isFile": true,
	"fs.list": true, "fs.listDir": true, "fs.mode": true, "fs.modified": true, "fs.readFile": true,
	"fs.readLines": true, "fs.readlink": true, "fs.size": true, "fs.stat": true, "fs.walk": true,

	"env.expand": true, "env.get": true, "env.getOr": true, "env.home": true, "env.list": true,
	"env.lookup": true, "env.require": true,

	"os.time": true, "os.hostname": true, "os.numCpu": true, "os.platform": true, "os.executable": true, "os.env": true,

	"vcs.base": true, "vcs.changedFiles": true, "vcs.commit": true, "vcs.commits": true,
	"vcs.describe": true, "vcs.diffStat": true, "vcs.dirtyDiff": true, "vcs.history": true,
	"vcs.ignoredFiles": true, "vcs.isDirty": true, "vcs.name": true, "vcs.ref": true,
	"vcs.regions": true, "vcs.root": true, "vcs.status": true, "vcs.tags": true,

	"magus.canonicalName": true, "magus.describe.file": true, "magus.describe.graph": true,
	"magus.describe.graphMarkdown": true, "magus.describe.project": true, "magus.describe.tool": true,
	"magus.explain": true, "magus.refs": true, "magus.query": true, "magus.neighborhood": true,
	"magus.symbols": true, "magus.unreferenced": true, "magus.stats": true, "magus.dir": true,
	"magus.dirs": true, "magus.layer": true, "magus.importGraph": true, "magus.ownership": true,
	"magus.trend": true, "magus.hotspots": true, "magus.affinity": true, "magus.precedents": true,
	"magus.workspace.root": true,
}

// mayRepeat reports whether the native name may run a second time after a run died
// inside it. Only reads qualify. A write, a process exec or a network call never does.
func mayRepeat(name string) bool {
	if repeatable[name] {
		return true
	}
	module, _, _ := strings.Cut(name, ".")
	return repeatable[module]
}
