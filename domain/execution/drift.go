package execution

import (
	"sort"
	"strings"
)

type FieldDiff struct {
	Added       []string `json:"added"`
	Missing     []string `json:"missing"`
	TypeChanged []string `json:"type_changed"`
}

func CompareFields(before, after []string) FieldDiff {
	old, next := map[string]string{}, map[string]string{}
	for _, f := range before {
		if i := strings.LastIndex(f, ":"); i >= 0 {
			old[f[:i]] = f[i+1:]
		}
	}
	for _, f := range after {
		if i := strings.LastIndex(f, ":"); i >= 0 {
			next[f[:i]] = f[i+1:]
		}
	}
	d := FieldDiff{}
	for path, kind := range next {
		if prior, ok := old[path]; !ok {
			d.Added = append(d.Added, path)
		} else if prior != kind {
			d.TypeChanged = append(d.TypeChanged, path+":"+prior+"->"+kind)
		}
	}
	for path := range old {
		if _, ok := next[path]; !ok {
			d.Missing = append(d.Missing, path)
		}
	}
	sort.Strings(d.Added)
	sort.Strings(d.Missing)
	sort.Strings(d.TypeChanged)
	return d
}
