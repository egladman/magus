package std

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTemplateRender(t *testing.T) {
	out, err := TemplateRender(context.Background(), "Hello {{name}}", map[string]any{"name": "world"})
	require.NoError(t, err)
	assert.Equal(t, "Hello world", out)
}

func TestTemplateRenderEscapesByDefault(t *testing.T) {
	out, err := TemplateRender(context.Background(), "{{html}}", map[string]any{"html": "<b>&</b>"})
	require.NoError(t, err)
	assert.Equal(t, "&lt;b&gt;&amp;&lt;/b&gt;", out)
}

func TestTemplateRenderPartials(t *testing.T) {
	tmpl := "<body>{{>header}}<main>{{content}}</main>{{>footer}}</body>"
	partials := map[string]string{
		"header": "<header>{{title}}</header>",
		"footer": "<footer>{{title}}</footer>",
	}
	out, err := TemplateRenderPartials(context.Background(), tmpl, map[string]any{
		"title":   "magus",
		"content": "hi",
	}, partials)
	require.NoError(t, err)
	assert.Equal(t, "<body><header>magus</header><main>hi</main><footer>magus</footer></body>", out)
}

// A partial may reference another partial; the StaticProvider resolves the chain.
func TestTemplateRenderPartialsNested(t *testing.T) {
	partials := map[string]string{
		"outer": "[{{>inner}}]",
		"inner": "{{v}}",
	}
	out, err := TemplateRenderPartials(context.Background(), "{{>outer}}", map[string]any{"v": "x"}, partials)
	require.NoError(t, err)
	assert.Equal(t, "[x]", out)
}

// An unresolved partial renders empty rather than erroring, matching the
// StaticProvider contract (missing name -> empty template).
func TestTemplateRenderPartialsMissing(t *testing.T) {
	out, err := TemplateRenderPartials(context.Background(), "a{{>gone}}b", nil, map[string]string{})
	require.NoError(t, err)
	assert.Equal(t, "ab", out)
}

func TestTemplateRenderPartialsMalformed(t *testing.T) {
	_, err := TemplateRenderPartials(context.Background(), "{{#open}}", nil, nil)
	require.Error(t, err)
}
