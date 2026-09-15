package chaptarr

import (
	"bytes"
	"encoding/json"
	"errors"
	"regexp"
	"sort"
	"strings"
)

// ProviderID accepts Chaptarr's string and numeric provider IDs without
// converting large integers through float64. Malformed metadata is not identity.
type ProviderID string

func (id *ProviderID) UnmarshalJSON(data []byte) error {
	*id = ""
	if bytes.Equal(data, []byte("null")) {
		return nil
	}
	var text string
	if json.Unmarshal(data, &text) == nil {
		*id = ProviderID(strings.TrimSpace(text))
	} else if digitsID.Match(data) {
		*id = ProviderID(string(data))
	}
	return nil
}

var digitsID = regexp.MustCompile(`^[0-9]+$`)
var olWorkID = regexp.MustCompile(`^OL[0-9]+W$`)
var olEditionID = regexp.MustCompile(`^OL[0-9]+M$`)

// IdentityKeys use separate namespaces for works and editions. In particular,
// gr:123 as a native book/work is NOT Goodreads edition 123. Native Chaptarr's
// BookResource.BuildForeignBookId uses hc/gr/ol work IDs (not facade IDs).
func (b Book) IdentityKeys() []string {
	keys := NativeBookIdentityKeys(b.ForeignBookID)
	add := func(kind, prefix string, id ProviderID, shape *regexp.Regexp) {
		if key := providerKey(kind, prefix, string(id), shape); key != "" {
			keys = append(keys, key)
		}
	}
	add("gr-work", "gr", b.GoodreadsWorkID, digitsID)
	add("gr-edition", "gr", b.GoodreadsBookID, digitsID)
	add("hc-book", "hc", b.HardcoverBookID, digitsID)
	add("ol-work", "ol", b.OpenLibraryWorkID, olWorkID)
	editions := append([]Edition{{ForeignEditionID: b.ForeignEditionID}}, b.Editions...)
	for _, e := range editions {
		add("gr-edition", "gr", e.GoodreadsEditionID, digitsID)
		add("ol-edition", "ol", e.OpenLibraryEditionID, olEditionID)
		hcEdition := strings.Replace(string(e.HardcoverEditionID), "hc:edition:", "hc:", 1)
		add("hc-edition", "hc", ProviderID(hcEdition), digitsID)
		// Only typed, validated foreign editions contribute an alias.
		if strings.HasPrefix(strings.ToLower(e.ForeignEditionID), "gr:") {
			add("gr-edition", "gr", ProviderID(e.ForeignEditionID), digitsID)
		}
		if strings.HasPrefix(strings.ToLower(e.ForeignEditionID), "ol:") {
			add("ol-edition", "ol", ProviderID(e.ForeignEditionID), olEditionID)
		}
		if strings.HasPrefix(strings.ToLower(e.ForeignEditionID), "hc:edition:") {
			add("hc-edition", "hc", ProviderID(e.ForeignEditionID[len("hc:edition:"):]), digitsID)
		}
		for _, isbn := range []string{e.ISBN13, e.ISBN10} {
			if valid := NormalizeISBN(isbn); valid != "" {
				keys = append(keys, "isbn:"+valid)
			}
		}
	}
	return UniqueIdentityKeys(keys)
}

func (b LookupResult) IdentityKeys() []string {
	book := Book{ForeignBookID: b.ForeignBookID, ForeignEditionID: b.ForeignEditionID, GoodreadsBookID: b.GoodreadsBookID,
		GoodreadsWorkID: b.GoodreadsWorkID, HardcoverBookID: b.HardcoverBookID, OpenLibraryWorkID: b.OpenLibraryWorkID}
	for _, raw := range b.Editions {
		var edition Edition
		if json.Unmarshal(raw, &edition) == nil {
			book.Editions = append(book.Editions, edition)
		}
	}
	return book.IdentityKeys()
}

func NativeBookIdentityKeys(id string) []string {
	id = strings.TrimSpace(id)
	if id == "" {
		return nil
	}
	keys := []string{"native-book:" + id}
	prefix, _, _ := strings.Cut(strings.ToLower(id), ":")
	var key string
	switch prefix {
	case "gr":
		key = providerKey("gr-work", "gr", id, digitsID)
	case "hc":
		key = providerKey("hc-book", "hc", id, digitsID)
	case "ol":
		key = providerKey("ol-work", "ol", id, olWorkID)
	}
	if key != "" {
		keys = append(keys, key)
	}
	return keys
}

func providerKey(kind, prefix, id string, shape *regexp.Regexp) string {
	id = strings.TrimSpace(id)
	if p, value, has := strings.Cut(id, ":"); has {
		if !strings.EqualFold(p, prefix) {
			return ""
		}
		id = value
	}
	id = strings.ToUpper(id)
	if !shape.MatchString(id) || id == "0" {
		return ""
	}
	if shape == digitsID {
		id = strings.TrimLeft(id, "0")
		if id == "" {
			return ""
		}
	}
	return kind + ":" + id
}

func UniqueIdentityKeys(keys []string) []string {
	set := make(map[string]bool, len(keys))
	for _, key := range keys {
		if key != "" {
			set[key] = true
		}
	}
	out := make([]string, 0, len(set))
	for key := range set {
		out = append(out, key)
	}
	sort.Strings(out)
	return out
}

// NormalizeISBN validates the checksum and returns ISBN-13, so a valid ISBN-10
// and its 978 equivalent identify the same publication. Junk never binds books.
func NormalizeISBN(isbn string) string {
	isbn = strings.ToUpper(strings.Join(strings.Fields(strings.ReplaceAll(isbn, "-", "")), ""))
	if len(isbn) == 10 {
		sum := 0
		for i, c := range isbn {
			n := int(c - '0')
			if i == 9 && c == 'X' {
				n = 10
			} else if n < 0 || n > 9 {
				return ""
			}
			sum += (10 - i) * n
		}
		if sum%11 != 0 {
			return ""
		}
		isbn = "978" + isbn[:9]
		sum = 0
		for i, c := range isbn {
			sum += int(c-'0') * (1 + 2*(i%2))
		}
		return isbn + string(rune('0'+(10-sum%10)%10))
	}
	if len(isbn) != 13 || (!strings.HasPrefix(isbn, "978") && !strings.HasPrefix(isbn, "979")) || !digitsID.MatchString(isbn) {
		return ""
	}
	sum := 0
	for i, c := range isbn {
		sum += int(c-'0') * (1 + 2*(i%2))
	}
	if sum%10 != 0 {
		return ""
	}
	return isbn
}

var ErrBookIdentityAmbiguous = errors.New("book identifiers match more than one library title")

// BookIdentityIndex never merges library records. The value of each key is the
// set of native title groups stating it. A match spanning two groups is unsafe,
// even if one key is unique or the titles happen to agree.
type BookIdentityIndex map[string][]string

func IndexBookIdentities(books []Book) BookIdentityIndex {
	index := BookIdentityIndex{}
	for _, book := range books {
		if book.ForeignBookID == "" {
			continue
		}
		for _, key := range book.IdentityKeys() {
			index[key] = UniqueIdentityKeys(append(index[key], book.ForeignBookID))
		}
	}
	return index
}

func (index BookIdentityIndex) Resolve(foreignID string, keys []string) (string, error) {
	foreignID = strings.TrimSpace(foreignID)
	if len(index["native-book:"+foreignID]) > 0 {
		return foreignID, nil
	}
	matches := map[string]bool{}
	for _, key := range append(NativeBookIdentityKeys(foreignID), keys...) {
		for _, id := range index[key] {
			matches[id] = true
		}
	}
	if len(matches) > 1 {
		return "", ErrBookIdentityAmbiguous
	}
	for id := range matches {
		return id, nil
	}
	return "", nil
}
