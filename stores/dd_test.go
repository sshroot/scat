package stores_test

import (
	"github.com/Roman2K/scat/stores"
	"testing"
)

func TestDd(t *testing.T) {
	dirStoreTest(func(dir stores.Dir) stores.Store {
		return stores.Dd{Dir: dir}
	}).run(t)
}
