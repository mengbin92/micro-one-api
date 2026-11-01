package data

import (
	"context"
	"crypto/sha256"
	"fmt"
	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
	"io"
	"micro-one-api/pkg/jsonx"
	"net"
	"net/http"
	"net/url"
	"os"
	"path"
	"strings"
	"time"
)

type S3Config struct {
	Endpoint     string `json:"endpoint"`
	Bucket       string `json:"bucket"`
	Prefix       string `json:"prefix"`
	Region       string `json:"region"`
	AccessKeyEnv string `json:"access_key_env"`
	SecretKeyEnv string `json:"secret_key_env"`
}
type ObjectStore struct {
	client *minio.Client
	cfg    S3Config
}

func NewObjectStore(c S3Config) (*ObjectStore, error) {
	u, err := url.Parse(c.Endpoint)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path != "" && u.Path != "/" {
		return nil, fmt.Errorf("invalid S3 endpoint")
	}
	ip := net.ParseIP(u.Hostname())
	if u.Scheme != "https" && !(u.Scheme == "http" && (u.Hostname() == "localhost" || ip != nil && ip.IsLoopback())) {
		return nil, fmt.Errorf("S3 requires TLS outside loopback")
	}
	if c.Bucket == "" || c.Prefix == "" || !safeName(c.Prefix) {
		return nil, fmt.Errorf("private S3 bucket and bounded prefix required")
	}
	ak, sk := os.Getenv(c.AccessKeyEnv), os.Getenv(c.SecretKeyEnv)
	if ak == "" || sk == "" {
		return nil, fmt.Errorf("S3 credential references unavailable")
	}
	client, err := minio.New(u.Host, &minio.Options{Creds: credentials.NewStaticV4(ak, sk, ""), Secure: u.Scheme == "https", Region: c.Region})
	return &ObjectStore{client: client, cfg: c}, err
}

// Preflight refuses publicly readable policies. Buckets must be provisioned
// separately; the worker does not create buckets or modify access policies.
func (s *ObjectStore) Preflight(ctx context.Context) error {
	exists, err := s.client.BucketExists(ctx, s.cfg.Bucket)
	if err != nil {
		return err
	}
	if !exists {
		return fmt.Errorf("backup bucket missing")
	}
	raw, err := s.client.GetBucketPolicy(ctx, s.cfg.Bucket)
	if err != nil {
		if minio.ToErrorResponse(err).Code == "NoSuchBucketPolicy" {
			return nil
		}
		return err
	}
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	var policy struct {
		Statement []struct {
			Effect    string
			Principal any
		}
	}
	if err = jsonx.Unmarshal([]byte(raw), &policy); err != nil {
		return err
	}
	for _, st := range policy.Statement {
		p, _ := jsonx.Marshal(st.Principal)
		if strings.EqualFold(st.Effect, "Allow") && strings.Contains(string(p), `"*"`) {
			return fmt.Errorf("public bucket policy rejected")
		}
	}
	return nil
}
func (s *ObjectStore) Key(id string) string { return path.Join(s.cfg.Prefix, id+".tar.gz.age") }
func (s *ObjectStore) Put(ctx context.Context, key, file, hash string) (int64, error) {
	if !s.validKey(key) {
		return 0, fmt.Errorf("invalid backup key")
	}
	f, err := os.Open(file) // #nosec G304 -- operator-configured local artifact path (upload) or worker scratch file.
	if err != nil {
		return 0, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return 0, err
	}
	opts := minio.PutObjectOptions{ContentType: "application/octet-stream", UserMetadata: map[string]string{"sha256": hash}}
	opts.SetMatchETagExcept("*")
	_, err = s.client.PutObject(ctx, s.cfg.Bucket, key, f, info.Size(), opts)
	if err != nil {
		return 0, err
	}
	out, err := s.client.StatObject(ctx, s.cfg.Bucket, key, minio.StatObjectOptions{})
	if err != nil || out.Size != info.Size() || out.UserMetadata["Sha256"] != hash {
		return 0, fmt.Errorf("S3 verification failed")
	}
	// Read-back proves persisted bytes, not merely echoed metadata.
	obj, e := s.client.GetObject(ctx, s.cfg.Bucket, key, minio.GetObjectOptions{})
	if e != nil {
		return 0, e
	}
	h := sha256.New()
	n, e := io.Copy(h, obj)
	ce := obj.Close()
	if e != nil || ce != nil || n != info.Size() || fmt.Sprintf("%x", h.Sum(nil)) != hash {
		return 0, fmt.Errorf("S3 content verification failed")
	}
	u, _ := url.Parse(s.cfg.Endpoint)
	u.Path = "/" + s.cfg.Bucket + "/" + key
	request, e := http.NewRequestWithContext(ctx, http.MethodHead, u.String(), nil)
	if e != nil {
		return 0, e
	}
	response, e := (&http.Client{Timeout: 15 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}).Do(request)
	if e != nil {
		return 0, e
	}
	response.Body.Close()
	if response.StatusCode != 401 && response.StatusCode != 403 {
		return 0, fmt.Errorf("backup object anonymous access not denied")
	}
	return info.Size(), nil
}
func (s *ObjectStore) validKey(key string) bool {
	return strings.HasPrefix(key, s.cfg.Prefix+"/") && safeName(key) && strings.HasSuffix(key, ".tar.gz.age") && path.Dir(key) == s.cfg.Prefix
}
func (s *ObjectStore) Delete(ctx context.Context, key string) error {
	if !s.validKey(key) {
		return fmt.Errorf("retention key outside configured prefix")
	}
	if err := s.client.RemoveObject(ctx, s.cfg.Bucket, key, minio.RemoveObjectOptions{}); err != nil {
		return err
	}
	id := strings.TrimSuffix(path.Base(key), ".tar.gz.age")
	return s.client.RemoveObject(ctx, s.cfg.Bucket, s.receiptKey(id), minio.RemoveObjectOptions{})
}
func (s *ObjectStore) Get(ctx context.Context, key, output, hash string) error {
	if !s.validKey(key) || len(hash) != 64 {
		return fmt.Errorf("backup receipt required")
	}
	obj, err := s.client.GetObject(ctx, s.cfg.Bucket, key, minio.GetObjectOptions{})
	if err != nil {
		return err
	}
	defer obj.Close()
	f, err := os.OpenFile(output, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600) // #nosec G304 -- operator-configured download destination on the private host worker.
	if err != nil {
		return err
	}
	defer f.Close()
	h := sha256.New()
	if _, err = io.Copy(io.MultiWriter(f, h), obj); err != nil {
		return err
	}
	if fmt.Sprintf("%x", h.Sum(nil)) != hash {
		return fmt.Errorf("encrypted artifact digest mismatch")
	}
	return f.Sync()
}

// ObjectReceipt is the external recovery index. It survives loss of the
// control database and contains no credentials, business rows or plaintext.
type ObjectReceipt struct {
	JobID      string `json:"job_id"`
	ObjectKey  string `json:"object_key"`
	SHA256     string `json:"sha256"`
	Bytes      int64  `json:"bytes"`
	SnapshotAt int64  `json:"snapshot_at"`
}

func (s *ObjectStore) receiptKey(id string) string {
	return path.Join(s.cfg.Prefix, id+".receipt.json")
}
func (s *ObjectStore) PublishReceipt(ctx context.Context, r ObjectReceipt) error {
	if !ident.MatchString(r.JobID) || r.ObjectKey != s.Key(r.JobID) || len(r.SHA256) != 64 || r.Bytes <= 0 {
		return fmt.Errorf("invalid recovery receipt")
	}
	raw, err := jsonx.Marshal(r)
	if err != nil {
		return err
	}
	opts := minio.PutObjectOptions{ContentType: "application/json"}
	opts.SetMatchETagExcept("*")
	_, err = s.client.PutObject(ctx, s.cfg.Bucket, s.receiptKey(r.JobID), strings.NewReader(string(raw)), int64(len(raw)), opts)
	if err != nil {
		return err
	}
	got, err := s.Receipt(ctx, r.JobID)
	if err != nil {
		return err
	}
	if got != r {
		return fmt.Errorf("recovery receipt verification failed")
	}
	return nil
}
func (s *ObjectStore) Receipt(ctx context.Context, id string) (ObjectReceipt, error) {
	var r ObjectReceipt
	if !ident.MatchString(id) {
		return r, fmt.Errorf("invalid recovery job id")
	}
	obj, err := s.client.GetObject(ctx, s.cfg.Bucket, s.receiptKey(id), minio.GetObjectOptions{})
	if err != nil {
		return r, err
	}
	defer obj.Close()
	raw, err := io.ReadAll(io.LimitReader(obj, 8193))
	if err != nil {
		return r, err
	}
	if len(raw) > 8192 {
		return r, fmt.Errorf("oversized recovery receipt")
	}
	if err = jsonx.Unmarshal(raw, &r); err != nil {
		return r, err
	}
	if r.JobID != id || r.ObjectKey != s.Key(id) || len(r.SHA256) != 64 || r.Bytes <= 0 {
		return r, fmt.Errorf("invalid recovery receipt")
	}
	return r, nil
}
func (s *ObjectStore) Catalog(ctx context.Context) ([]ObjectReceipt, error) {
	out := []ObjectReceipt{}
	for item := range s.client.ListObjects(ctx, s.cfg.Bucket, minio.ListObjectsOptions{Prefix: s.cfg.Prefix + "/", Recursive: false}) {
		if item.Err != nil {
			return nil, item.Err
		}
		if !strings.HasSuffix(item.Key, ".receipt.json") {
			continue
		}
		id := strings.TrimSuffix(strings.TrimPrefix(item.Key, s.cfg.Prefix+"/"), ".receipt.json")
		r, err := s.Receipt(ctx, id)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
		if len(out) >= 1000 {
			return nil, fmt.Errorf("recovery catalog exceeds 1000 receipts; narrow storage prefix")
		}
	}
	return out, nil
}

// Retention is scoped to the immutable storage namespace, independent of
// credential rotation. Changing a bucket/prefix never deletes old targets.
func (s *ObjectStore) ID() string {
	raw, _ := jsonx.Marshal([]string{s.cfg.Endpoint, s.cfg.Bucket, s.cfg.Prefix})
	return fmt.Sprintf("%x", sha256.Sum256(raw))
}
