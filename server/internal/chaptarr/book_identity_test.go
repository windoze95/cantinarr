package chaptarr

import (
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"testing"
)

func TestBookIdentitySharedCases(t *testing.T) {
	data, err := os.ReadFile("../../../testdata/book_identity.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Cases []struct {
			Name      string          `json:"name"`
			Selected  json.RawMessage `json:"selected"`
			Library   []Book          `json:"library"`
			Expected  string          `json:"expected"`
			Ambiguous bool            `json:"ambiguous"`
		} `json:"cases"`
	}
	if err := json.Unmarshal(data, &fixture); err != nil {
		t.Fatal(err)
	}
	for _, tt := range fixture.Cases {
		t.Run(tt.Name, func(t *testing.T) {
			var selected Book
			var lookup LookupResult
			if err := json.Unmarshal(tt.Selected, &selected); err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(tt.Selected, &lookup); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(selected.IdentityKeys(), lookup.IdentityKeys()) {
				t.Fatal("library and lookup decoders lost different identifiers")
			}
			got, err := IndexBookIdentities(tt.Library).Resolve(selected.ForeignBookID, selected.IdentityKeys())
			if got != tt.Expected || errors.Is(err, ErrBookIdentityAmbiguous) != tt.Ambiguous {
				t.Fatalf("got %q, %v; want %q, ambiguous=%v", got, err, tt.Expected, tt.Ambiguous)
			}
		})
	}
}

func TestProviderIDsPreserveLargeNumbersAndIgnoreMalformedMetadata(t *testing.T) {
	var book Book
	if err := json.Unmarshal([]byte(`{"goodreadsWorkId":987654321098765432,"hardcoverBookId":{},"goodreadsBookId":null}`), &book); err != nil {
		t.Fatal(err)
	}
	if book.GoodreadsWorkID != "987654321098765432" || book.HardcoverBookID != "" || book.GoodreadsBookID != "" {
		t.Fatalf("bad IDs: %+v", book)
	}
}
