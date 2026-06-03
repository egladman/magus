// Amalgamation of the Umka library sources, so cgo can build Umka as a single
// translation unit without loose .c files in the Go package directory (Go
// rejects those when cgo is off, which would break the default pure-Go build).
//
// umka.c (the standalone CLI's main) is intentionally excluded. The lexer and
// types modules each define a file-local `static spelling[]` table; in a unity
// build those collide, so each is renamed via the macro below. The rename is
// inert — they are private lookup tables.
//
// Vendored from github.com/vtereshkov/umka-lang v1.5.6 (BSD-2-Clause; see LICENSE).
#include "umka_common.c"
#include "umka_runtime.c"

#define spelling spelling_lexer
#include "umka_lexer.c"
#undef spelling

#define spelling spelling_types
#include "umka_types.c"
#undef spelling

#include "umka_ident.c"
#include "umka_const.c"
#include "umka_vm.c"
#include "umka_gen.c"
#include "umka_expr.c"
#include "umka_decl.c"
#include "umka_stmt.c"
#include "umka_compiler.c"
#include "umka_api.c"
