package doctor

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/egladman/magus/internal/agent/model"
	"github.com/egladman/magus/internal/config"
	"github.com/egladman/magus/types"
)

func TestCheckModelTable(t *testing.T) {
	fresh := checkModelTable(model.AsOf.Add(model.MaxAge), config.Agents{})
	assert.Equal(t, types.CheckOK, fresh.Status)
	assert.Equal(t, "model-table", fresh.Name)

	stale := checkModelTable(model.AsOf.Add(model.MaxAge+48*time.Hour), config.Agents{
		Models: map[string]config.AgentModel{"claude-next-1": {Window: 1}},
	})
	assert.Equal(t, types.CheckAdvice, stale.Status, "a stale table breaks nothing, so it advises")
	assert.Contains(t, stale.Message, "122 days ago")
	assert.Contains(t, stale.Details, "1 agents.models override(s) in magus.yaml")
}
