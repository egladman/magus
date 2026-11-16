package buzz

import "fmt"

// DeclareNamespaceTypes is DeclareModuleTypes for a nested namespace such as
// shop\order. Its types are nameable only qualified, as `import "shop/order";`
// declares them, so none shadows a bare name. Nothing runs and nothing is
// bound: the host binds each object's runtime definition under
// QualifiedTypeKey(ns, name). A source that does not parse is an error.
func (s *Session) DeclareNamespaceTypes(ns, src string) error {
	if err := s.declareHostTypes(ns, src, false); err != nil {
		return fmt.Errorf("gopherbuzz: DeclareNamespaceTypes(%q): %w", ns, err)
	}
	return nil
}
