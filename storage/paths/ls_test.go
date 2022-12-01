package paths

import (
	"fmt"
	"testing"
)

func TestReadDir(t *testing.T) {
	names, err := ReadDir("/data/zfs/ipfs/datastore")
	if err != nil {
		t.Fatal(err)
	}
	fmt.Println(names)

	for _, name := range names {
		fmt.Println(name, ",", len(name))
	}
}
