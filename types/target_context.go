package types

// ContextParamAnnot is the exact parameter type annotation that marks a target: an
// exported magusfile function is a target if and only if its FIRST parameter carries
// this annotation. Buzz namespaces a qualified type with a backslash
// (`serialize\Boxed`), not a dot, so the magus context type is spelled `magus\Context`
// in a magusfile; a dotted `magus.Context` is not valid Buzz type syntax (the parser
// stops at the dot). Recognition keys on this raw annotation string, independent of
// whether the checker can resolve the type (it treats an unknown qualified name
// permissively). A ctx-less exported function is rejected at load (MGS1008).
const ContextParamAnnot = `magus\Context`
