package repository

import (
	dbaccount "github.com/Wei-Shaw/sub2api/ent/account"
	dbpredicate "github.com/Wei-Shaw/sub2api/ent/predicate"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

// cursorOAuthOnlyAccountPredicate keeps legacy Cursor rows with unsupported
// account types out of every schedulable account pool. Non-Cursor platforms
// retain their existing account-type behavior.
func cursorOAuthOnlyAccountPredicate() dbpredicate.Account {
	return dbaccount.Or(
		dbaccount.PlatformNEQ(service.PlatformCursor),
		dbaccount.TypeEQ(service.AccountTypeOAuth),
	)
}

// cursorOAuthOnlyAccountSQL is the raw-SQL equivalent of
// cursorOAuthOnlyAccountPredicate. columnPrefix is a trusted, static table
// alias such as "a.".
func cursorOAuthOnlyAccountSQL(columnPrefix string) string {
	return "(" + columnPrefix + "platform <> '" + service.PlatformCursor +
		"' OR " + columnPrefix + "type = '" + service.AccountTypeOAuth + "')"
}
