package doctor

import (
	"fmt"
	"time"

	"github.com/egladman/magus/internal/agent/model"
	"github.com/egladman/magus/internal/config"
	"github.com/egladman/magus/types"
)

const modelTableCheck = "model-table"

// checkModelTable reports a shipped model table older than model.MaxAge. A model released
// after it matches no row, so the guard's cost advice for it falls back to token counts,
// with no window or price behind them. It advises rather than fails: nothing is broken,
// and agents.models fills the gap without a new magus.
func (r *runner) checkModelTable() types.Check {
	return checkModelTable(time.Now(), r.opts.cfg.Agents)
}

func checkModelTable(now time.Time, agents config.Agents) types.Check {
	asOf := model.AsOf.Format(time.DateOnly)
	overrides := len(agents.Models)
	if !model.Stale(now) {
		return types.Check{Name: modelTableCheck, Status: types.CheckOK,
			Message: fmt.Sprintf("model table read %s, %d agents.models override(s)", asOf, overrides)}
	}
	days := int(now.Sub(model.AsOf).Hours() / 24)
	return types.Check{
		Name:    modelTableCheck,
		Status:  types.CheckAdvice,
		Message: fmt.Sprintf("the model table this magus ships was read %s, %d days ago; a model released since matches no row", asOf, days),
		Details: []string{
			fmt.Sprintf("%d agents.models override(s) in magus.yaml", overrides),
			"source: " + model.Source,
			"update magus, or add the model under agents.models in magus.yaml with its window, cache_read, price_step and step_factor",
		},
	}
}
