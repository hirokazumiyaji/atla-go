package atla

import (
	"context"
	"database/sql"
	"testing"
)

var (
	_ Executor   = (*sql.DB)(nil)
	_ Executor   = (*sql.Tx)(nil)
	_ Executor   = (*sql.Conn)(nil)
	_ TxBeginner = (*sql.DB)(nil)
	_ TxBeginner = (*sql.Conn)(nil)
)

func TestQueryInfoRoundTrip(t *testing.T) {
	ctx := context.Background()
	if qi, ok := QueryInfoFrom(ctx); ok {
		t.Fatalf("QueryInfoFrom(empty) = %+v, true; want false", qi)
	}
	want := QueryInfo{Op: "select", Table: "users", ReadOnly: true}
	got, ok := QueryInfoFrom(withQueryInfo(ctx, want))
	if !ok || got != want {
		t.Fatalf("QueryInfoFrom = %+v, %v; want %+v, true", got, ok, want)
	}
}
