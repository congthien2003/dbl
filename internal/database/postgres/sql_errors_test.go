package postgres

import (
	"context"
	"strings"
	"testing"

	"dbl/internal/database"
	"github.com/jackc/pgx/v5/pgconn"
)

func TestSQLExecutionErrorCategoriesAndSecrets(t *testing.T) {
	for _, test := range []struct {
		code string
		kind database.Kind
	}{
		{"42501", database.KindPermission}, {"42601", database.KindSyntax}, {"23505", database.KindValidation},
		{"25P02", database.KindValidation}, {"40001", database.KindConflict}, {"40P01", database.KindConflict}, {"0A000", database.KindUnsupported},
	} {
		err := classifySQLExecution(context.Background(), &pgconn.PgError{Code: test.code, Message: "secret-password"}, "secret-password")
		if err.Kind != test.kind || strings.Contains(err.Message, "secret-password") {
			t.Fatalf("%s -> %+v", test.code, err)
		}
	}
}
