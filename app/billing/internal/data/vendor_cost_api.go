package data

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"micro-one-api/app/billing/internal/biz"
	"micro-one-api/pkg/jsonx"
)

// Credentials stay in data and never enter a report or error body. The caller
// owns the shared client; no redirects may transfer an admin credential.
type vendorCostReader struct {
	client *http.Client
	key    string
}

func (r *vendorReconciliationRepo) ValidateCostEvidence(ctx context.Context, e biz.VendorCostEvidence) (biz.VendorCostEvidence, error) {
	if err := vendorReportAuth(ctx, "billing.vendor_report.import"); err != nil {
		return biz.VendorCostEvidence{}, err
	}
	if e.FetchedAt.IsZero() || e.FetchedAt.After(time.Now().Add(time.Minute)) {
		return biz.VendorCostEvidence{}, fmt.Errorf("invalid cost evidence capture time")
	}
	replayed, err := ReplayVendorCostPages(e.Query, e.Pages, e.FetchedAt)
	if err != nil {
		return replayed, err
	}
	if replayed.ID() != e.ID() || !e.PagesComplete || e.CSVMatched != replayed.CSVMatched {
		return replayed, fmt.Errorf("cost evidence replay changed")
	}
	// Confirm the reference in owner storage, not a caller-provided total.
	var reference vendorReportModel
	if err := r.data.db.WithContext(ctx).Where("id = ?", e.Query.CSVReportID).First(&reference).Error; err != nil {
		return replayed, fmt.Errorf("independent CSV reference not imported")
	}
	var csv biz.VendorReconciliation
	if jsonx.Unmarshal([]byte(reference.Payload), &csv) != nil || csv.CostAPIEvidence != nil || e.Query.ValidateCSV(csv.Manifest, csv.Rows) != nil {
		return replayed, fmt.Errorf("invalid independent CSV reference")
	}
	return replayed, nil
}

func NewVendorCostAPIData(clients ...*http.Client) *Data {
	client := &http.Client{Timeout: 20 * time.Second}
	if len(clients) > 0 && clients[0] != nil {
		client = clients[0]
	}
	return &Data{vendorCostClient: client}
}

func NewVendorCostReader(d *Data, key string) biz.VendorCostReader {
	copy := *d.vendorCostClient
	if copy.Timeout <= 0 {
		copy.Timeout = 20 * time.Second
	}
	copy.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &vendorCostReader{client: &copy, key: key}
}

func (r *vendorCostReader) FetchCosts(ctx context.Context, q biz.VendorCostQuery) (biz.VendorCostEvidence, error) {
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	if err := biz.ValidateVendorCostQuery(q, time.Now()); err != nil {
		return biz.VendorCostEvidence{}, err
	}
	if r.key == "" || strings.ContainsAny(r.key, "\r\n") {
		return biz.VendorCostEvidence{}, fmt.Errorf("dedicated cost API credential required")
	}
	var pages []biz.VendorCostPage
	totalBytes := 0
	cursor := ""
	seen := map[string]bool{}
	for i := 0; i < 64; i++ {
		endpoint := "https://api.openai.com/v1/organization/costs"
		query := url.Values{"start_time": {strconv.FormatInt(q.Scope.Start.Unix(), 10)}, "end_time": {strconv.FormatInt(q.Scope.End.Unix(), 10)}, "bucket_width": {"1d"}, "limit": {"7"}, "group_by": {"project_id", "line_item"}}
		if q.Scope.Project != "" {
			query.Set("project_ids", q.Scope.Project)
		}
		if q.Scope.Provider == "anthropic" {
			endpoint = "https://api.anthropic.com/v1/organizations/cost_report"
			query = url.Values{"starting_at": {q.Scope.Start.UTC().Format(time.RFC3339)}, "ending_at": {q.Scope.End.UTC().Format(time.RFC3339)}, "bucket_width": {"1d"}, "limit": {"7"}, "group_by[]": {"workspace_id", "description"}}
			// Cost Report supports grouping, not a workspace filter. Keep the
			// complete grouped archive and select the exact workspace on replay.
		}
		if cursor != "" {
			query.Set("page", cursor)
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint+"?"+query.Encode(), nil)
		if err != nil {
			return biz.VendorCostEvidence{}, fmt.Errorf("invalid cost API request")
		}
		if q.Scope.Provider == "openai" {
			req.Header.Set("Authorization", "Bearer "+r.key)
			req.Header.Set("OpenAI-Organization", q.Scope.Account)
		} else {
			req.Header.Set("x-api-key", r.key)
			req.Header.Set("anthropic-version", "2023-06-01")
		}
		resp, err := r.client.Do(req)
		if err != nil {
			return biz.VendorCostEvidence{}, fmt.Errorf("cost API transport failed")
		}
		raw, readErr := io.ReadAll(io.LimitReader(resp.Body, (1<<20)+1))
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return biz.VendorCostEvidence{}, fmt.Errorf("cost API HTTP %d; incomplete report", resp.StatusCode)
		}
		if readErr != nil || len(raw) > 1<<20 {
			return biz.VendorCostEvidence{}, fmt.Errorf("cost API page unavailable or too large")
		}
		totalBytes += len(raw)
		if totalBytes > 2<<20 {
			return biz.VendorCostEvidence{}, fmt.Errorf("cost API archive budget exhausted")
		}
		p, err := decodeCostPage(raw)
		if err != nil {
			return biz.VendorCostEvidence{}, err
		}
		pages = append(pages, biz.VendorCostPage{Body: raw, Digest: costPageHash(raw)})
		if !*p.HasMore {
			return ReplayVendorCostPages(q, pages, time.Now().UTC())
		}
		if p.NextPage == nil || *p.NextPage == "" || len(*p.NextPage) > 2048 || seen[*p.NextPage] {
			return biz.VendorCostEvidence{}, fmt.Errorf("invalid or repeated cost API cursor")
		}
		cursor = *p.NextPage
		seen[cursor] = true
	}
	return biz.VendorCostEvidence{}, fmt.Errorf("cost API page budget exhausted")
}

type costPage struct {
	Data     []costBucket `json:"data"`
	HasMore  *bool        `json:"has_more"`
	NextPage *string      `json:"next_page"`
}
type costBucket struct {
	Start      int64      `json:"start_time"`
	End        int64      `json:"end_time"`
	StartingAt string     `json:"starting_at"`
	EndingAt   string     `json:"ending_at"`
	Results    []costItem `json:"results"`
}
type costItem struct {
	Amount    jsonx.RawMessage `json:"amount"`
	Currency  string           `json:"currency"`
	Project   *string          `json:"project_id"`
	Workspace *string          `json:"workspace_id"`
}

func costPageHash(raw []byte) string { return fmt.Sprintf("%x", sha256.Sum256(raw)) }
func decodeCostPage(raw []byte) (costPage, error) {
	var p costPage
	if err := jsonx.ValidateUnique(raw); err != nil {
		return p, err
	}
	d := jsonx.NewDecoder(bytes.NewReader(raw))
	if len(raw) > 1<<20 || d.Decode(&p) != nil || p.HasMore == nil || p.Data == nil {
		return p, fmt.Errorf("invalid cost API page schema")
	}
	var extra any
	if d.Decode(&extra) != io.EOF {
		return p, fmt.Errorf("trailing cost API JSON")
	}
	if !*p.HasMore && p.NextPage != nil && *p.NextPage != "" {
		return p, fmt.Errorf("inconsistent terminal cursor")
	}
	return p, nil
}

// ReplayVendorCostPages uses archived supplier bytes, preserving decimal input
// precision. All daily buckets must be present exactly once. A completed page
// sequence still says nothing about invoice finality or provider watermark.
func ReplayVendorCostPages(q biz.VendorCostQuery, pages []biz.VendorCostPage, now time.Time) (biz.VendorCostEvidence, error) {
	out := biz.VendorCostEvidence{Query: q, Pages: pages, FetchedAt: now}
	if err := biz.ValidateVendorCostQuery(q, now); err != nil {
		return out, err
	}
	if len(pages) == 0 || len(pages) > 64 {
		return out, fmt.Errorf("bounded nonempty page archive required")
	}
	total := new(big.Rat)
	totalBytes := 0
	buckets, cursors := map[int64]bool{}, map[string]bool{}
	for i, page := range pages {
		totalBytes += len(page.Body)
		if totalBytes > 2<<20 {
			return out, fmt.Errorf("cost archive budget exceeded")
		}
		if page.Digest != costPageHash(page.Body) {
			return out, fmt.Errorf("cost page digest mismatch")
		}
		p, err := decodeCostPage(page.Body)
		if err != nil {
			return out, err
		}
		if *p.HasMore != (i < len(pages)-1) {
			return out, fmt.Errorf("incomplete cost page sequence")
		}
		if *p.HasMore {
			if p.NextPage == nil || *p.NextPage == "" || len(*p.NextPage) > 2048 || cursors[*p.NextPage] {
				return out, fmt.Errorf("invalid cost page cursor")
			}
			cursors[*p.NextPage] = true
		}
		for _, b := range p.Data {
			start, end := time.Unix(b.Start, 0).UTC(), time.Unix(b.End, 0).UTC()
			if q.Scope.Provider == "anthropic" {
				start, err = time.Parse(time.RFC3339, b.StartingAt)
				if err != nil {
					return out, fmt.Errorf("invalid cost bucket time")
				}
				end, err = time.Parse(time.RFC3339, b.EndingAt)
				if err != nil {
					return out, fmt.Errorf("invalid cost bucket time")
				}
			}
			if start.Before(q.Scope.Start) || end.After(q.Scope.End) || end.Sub(start) != 24*time.Hour || start.Unix()%86400 != 0 || start.Nanosecond() != 0 || buckets[start.Unix()] || b.Results == nil {
				return out, fmt.Errorf("missing, duplicate or out-of-scope daily cost bucket")
			}
			buckets[start.Unix()] = true
			for _, item := range b.Results {
				project := item.Project
				currency := item.Currency
				amount := ""
				var number *big.Rat
				if q.Scope.Provider == "openai" {
					var a struct {
						Value    jsonx.RawMessage `json:"value"`
						Currency string           `json:"currency"`
					}
					if jsonx.Unmarshal(item.Amount, &a) != nil {
						return out, fmt.Errorf("invalid cost amount")
					}
					amount, currency = string(a.Value), a.Currency
				} else {
					project = item.Workspace
					if jsonx.Unmarshal(item.Amount, &amount) != nil {
						return out, fmt.Errorf("invalid cent amount")
					}
				}
				if strings.ToUpper(currency) != q.Scope.Currency {
					return out, fmt.Errorf("cost currency mismatch")
				}
				if q.Scope.Project != "" {
					if project == nil || *project == "" {
						return out, fmt.Errorf("unassigned cost cannot establish exact project scope")
					}
					if *project != q.Scope.Project {
						if q.Scope.Provider == "anthropic" {
							continue
						}
						return out, fmt.Errorf("cost project scope mismatch")
					}
				}
				if q.Scope.Provider == "openai" {
					number, err = exactCostJSONNumber([]byte(amount))
					if err != nil {
						return out, err
					}
				} else {
					v, err := biz.VendorAmountTotal([]biz.VendorBillRow{{Amount: &amount}})
					if err != nil {
						return out, fmt.Errorf("invalid cost decimal")
					}
					number, _ = new(big.Rat).SetString(v)
				}
				if q.Scope.Provider == "anthropic" {
					number.Quo(number, big.NewRat(100, 1))
				}
				total.Add(total, number)
			}
		}
	}
	if len(buckets) != int(q.Scope.End.Sub(q.Scope.Start)/(24*time.Hour)) {
		return out, fmt.Errorf("daily cost coverage incomplete")
	}
	// The existing report contract supports up to 18 decimal major units.
	amount := strings.TrimRight(strings.TrimRight(total.FloatString(18), "0"), ".")
	back, _ := new(big.Rat).SetString(amount)
	if back.Cmp(total) != 0 {
		return out, fmt.Errorf("cost precision exceeds report contract")
	}
	out.Rows = []biz.VendorBillRow{{Amount: &amount}}
	out.PagesComplete = true
	csv, _ := new(big.Rat).SetString(q.CSVTotal)
	out.CSVMatched = csv.Cmp(total) == 0
	return out, nil
}

// JSON numbers may use scientific notation. Preserve it as a rational instead
// of decoding to float64; bound the exponent before allocating a big power.
func exactCostJSONNumber(raw []byte) (*big.Rat, error) {
	if len(raw) > 80 {
		return nil, fmt.Errorf("cost number budget exceeded")
	}
	var value any
	d := jsonx.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	if d.Decode(&value) != nil {
		return nil, fmt.Errorf("invalid cost number")
	}
	n, ok := value.(jsonx.Number)
	if !ok {
		return nil, fmt.Errorf("numeric cost value required")
	}
	text := n.String()
	if i := strings.IndexAny(text, "eE"); i >= 0 {
		exponent, err := strconv.Atoi(text[i+1:])
		if err != nil || exponent < -18 || exponent > 18 {
			return nil, fmt.Errorf("cost exponent exceeds precision budget")
		}
	}
	v, ok := new(big.Rat).SetString(text)
	if !ok {
		return nil, fmt.Errorf("invalid exact cost number")
	}
	return v, nil
}
