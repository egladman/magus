package access

import (
	"context"
	"fmt"
	"strings"

	"github.com/egladman/magus/libs/safetyscissors/verdict"
)

type clerk struct{ Team string }

type invoice struct {
	Team  string
	Total int
}

type pay = Request[clerk, invoice, struct{}]

func ExampleTable_Judge() {
	ownTeam := func(_ context.Context, r pay) (bool, error) { return r.Principal.Team == r.Target.Team, nil }
	large := func(_ context.Context, r pay) (bool, error) { return r.Target.Total > 1000, nil }
	audited := func(_ context.Context, r pay) (bool, error) { return r.Target.Team == "audit", nil }

	accounts, err := New(Bundle[clerk, invoice, struct{}]{
		Name: "accounts",
		Acts: []Act{"pay"},
		Rules: []Rule[clerk, invoice, struct{}]{
			Permit("pay-own-team", "pay", "the clerk is on the invoice's team", ownTeam),
			Forbid("large-invoice", "pay", verdict.Ask, "an invoice over 1000 needs a person", large),
		},
	})
	if err != nil {
		fmt.Println(err)
		return
	}
	withAudit, err := accounts.Extend(Bundle[clerk, invoice, struct{}]{
		Name: "audit",
		Rules: []Rule[clerk, invoice, struct{}]{
			Forbid("audit-freeze", "pay", verdict.Deny, "audit's invoices are frozen", audited),
		},
	})
	if err != nil {
		fmt.Println(err)
		return
	}

	ctx := context.Background()
	var answers []string
	for _, r := range []pay{
		{Principal: clerk{Team: "ops"}, Act: "pay", Target: invoice{Team: "ops", Total: 200}},
		{Principal: clerk{Team: "ops"}, Act: "pay", Target: invoice{Team: "ops", Total: 5000}},
		{Principal: clerk{Team: "ops"}, Act: "pay", Target: invoice{Team: "sales", Total: 200}},
		{Principal: clerk{Team: "audit"}, Act: "pay", Target: invoice{Team: "audit", Total: 200}},
	} {
		v, err := withAudit.Judge(ctx, r)
		if err != nil {
			fmt.Println(err)
			return
		}
		answers = append(answers, fmt.Sprintf("%s(%s)", v.Decision, v.Rule))
	}
	fmt.Println(strings.Join(answers, " "))
	// Output: unset() ask(large-invoice) deny() deny(audit-freeze)
}
