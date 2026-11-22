package cleanup

import (
	"database/sql"
	"errors"
	"testing"
)

type release func() error

func (r release) Close() error    { return r() }
func (r release) Rollback() error { return r() }

func TestReleaseErrors(t *testing.T) {
	primary, secondary := errors.New("operation"), errors.New("cleanup")
	for _, check := range []func(*error, release){
		func(err *error, r release) { Close(err, r) },
		func(err *error, r release) { Rollback(err, r) },
	} {
		err := primary
		check(&err, func() error { return nil })
		if err != primary {
			t.Fatal("successful cleanup changed original error")
		}
		check(&err, func() error { return secondary })
		if !errors.Is(err, primary) || !errors.Is(err, secondary) {
			t.Fatal(err)
		}
	}
	var err error
	Rollback(&err, release(func() error { return sql.ErrTxDone }))
	if err != nil {
		t.Fatal(err)
	}
}
