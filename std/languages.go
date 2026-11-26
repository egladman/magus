package std

import (
	"context"
	"slices"

	"github.com/egladman/magus/spells"
)

type languagesKey struct{}

// WithLanguages returns ctx carrying the languages the resolved spells declare, in spell
// order. A spell that declares none contributes nothing.
func WithLanguages(ctx context.Context, resolved []*spells.Spell) context.Context {
	var langs []spells.Language
	for _, s := range resolved {
		if s.Language() == "" {
			continue
		}
		langs = append(langs, spells.Language{
			Name:       s.Language(),
			Extensions: s.LanguageExtensions(),
			Syntax:     s.Syntax(),
		})
	}
	return context.WithValue(ctx, languagesKey{}, langs)
}

// LanguagesFromContext is the languages WithLanguages put on ctx. False means none were
// put there, which is not a workspace without languages: refuse on it, never read it as empty.
func LanguagesFromContext(ctx context.Context) ([]spells.Language, bool) {
	langs, ok := ctx.Value(languagesKey{}).([]spells.Language)
	return slices.Clone(langs), ok
}
