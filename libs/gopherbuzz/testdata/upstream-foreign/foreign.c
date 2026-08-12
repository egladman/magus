// An ABI-compatible reimplementation of upstream buzz's tests/utils/foreign.zig,
// the fixture library its ffi.buzz and types-as-value.buzz zdef against.
//
// Why this file exists: that library is compiled by upstream's `build.zig` and
// ships in no release, so the two tests failed on a MISSING ARTIFACT rather than on
// anything about this VM. The source is ordinary C ABI - no buzz import, not linked
// against libbuzz - so an equivalent C library is indistinguishable to a caller, and
// building it needs only `cc`. (Its siblings hello.zig and buzz_c_api.c are NOT like
// this: they take a `*NativeCtx` and link libbuzz, which is why those two tests stay
// out of reach. See the README.)
//
// The library is a FIXTURE, not the subject. What ffi.buzz actually exercises is
// gopherbuzz's own zdef path: dlopen, dlsym, argument marshalling, struct field
// layout and union aliasing. Those are all genuinely tested against this, because
// the ABI is the contract - a field order or width that did not match upstream's
// would surface as a wrong assertion, not as a silent pass.
//
// Keep the layouts byte-identical to foreign.zig:
//     Data { i32 id; char *msg; f64 value; }
//     Flag { i32 id; bool value; }
//     Misc = union { i32 id; Data data; Flag flag; }

#include <math.h>
#include <stdbool.h>
#include <stdint.h>
#include <stdio.h>

// Upstream exports its own `acos`, so this library must too. It cannot simply call
// acos(): on a platform that binds the call to this library's own definition that
// recurses forever. The atan2 identity avoids the self-reference and is bit-exact
// against libm here - ffi.buzz asserts the exact double 1.4505064444001086, which
// would fail loudly if it ever stopped being.
double acos(double value) { return atan2(sqrt(1.0 - value * value), value); }

void fprint(const char *msg) { printf("%s\n", msg); }

int32_t sum(const int32_t *values, int32_t len) {
  int32_t total = 0;
  for (int32_t i = 0; i < len; i++) {
    total += values[i];
  }
  return total;
}

typedef struct {
  int32_t id;
  char *msg;
  double value;
} Data;

char *get_data_msg(Data *data) { return data->msg; }

// Doubles the id in place, which is what lets ffi.buzz assert that a foreign
// function can MUTATE a struct passed by pointer (123 -> 42 -> 84).
void set_data_id(Data *data) { data->id *= 2; }

typedef struct {
  int32_t id;
  bool value;
} Flag;

typedef union {
  int32_t id;
  Data data;
  Flag flag;
} Misc;

char *get_misc_msg(Misc *misc) { return misc->data.msg; }

bool get_misc_flag(Misc *misc) { return misc->flag.value; }

void set_misc_id(Misc *misc, int32_t new_id) { misc->id = new_id; }
