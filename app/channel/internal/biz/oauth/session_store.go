package oauth

import (
	"sync"
	"time"
)

const defaultSessionTTL = 5 * time.Minute

type Session struct {
	CredentialDigest string
	Group            string
	ID               string
	Platform         string
	State            string
	CodeVerifier     string
	RedirectURI      string
	CreatedAt        time.Time
	Device           *DeviceSession `json:",omitempty"`
}

type DeviceSession struct {
	Code      string
	ClientID  string
	ExpiresAt time.Time
	PollAt    time.Time
	Interval  time.Duration
}

type SessionRepository interface {
	Set(*Session) error
	Pop(string, time.Time) (*Session, bool)
	Get(string, time.Time) (*Session, bool)
	CompareAndSwap(current, next *Session, now time.Time) (bool, error)
	Delete(string)
	Cleanup(time.Time)
	TTL() time.Duration
}

func (s *Session) ExpiresAt(ttl time.Duration) time.Time {
	end := s.CreatedAt.Add(ttl)
	if s.Device != nil && s.Device.ExpiresAt.Before(end) {
		end = s.Device.ExpiresAt
	}
	return end
}

func (s *SessionStore) TTL() time.Duration { return s.ttl }

type SessionStore struct {
	mu       sync.RWMutex
	ttl      time.Duration
	sessions map[string]*Session
}

func NewSessionStore(ttl time.Duration) *SessionStore {
	if ttl <= 0 {
		ttl = defaultSessionTTL
	}
	return &SessionStore{
		ttl:      ttl,
		sessions: make(map[string]*Session),
	}
}

func (s *SessionStore) Set(session *Session) error {
	if s == nil || session == nil || session.ID == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, previous := range s.sessions {
		if !session.CreatedAt.Before(previous.ExpiresAt(s.ttl)) {
			delete(s.sessions, id)
		}
	}
	s.sessions[session.ID] = session
	return nil
}

func (s *SessionStore) Pop(id string, now time.Time) (*Session, bool) {
	if s == nil || id == "" {
		return nil, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	session, ok := s.sessions[id]
	if !ok {
		return nil, false
	}
	delete(s.sessions, id)
	if now.IsZero() {
		now = time.Now()
	}
	if !now.Before(session.ExpiresAt(s.ttl)) {
		return nil, false
	}
	return session, true
}

func (s *SessionStore) Get(id string, now time.Time) (*Session, bool) {
	if s == nil || id == "" {
		return nil, false
	}
	s.mu.RLock()
	session, ok := s.sessions[id]
	s.mu.RUnlock()
	if !ok {
		return nil, false
	}
	if now.IsZero() {
		now = time.Now()
	}
	if !now.Before(session.ExpiresAt(s.ttl)) {
		return nil, false
	}
	return session, true
}

// CompareAndSwap replaces a session snapshot or consumes it when next is nil.
func (s *SessionStore) CompareAndSwap(current, next *Session, now time.Time) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.sessions[current.ID] != current || !now.Before(current.ExpiresAt(s.ttl)) {
		return false, nil
	}
	if next == nil {
		delete(s.sessions, current.ID)
	} else {
		s.sessions[current.ID] = next
	}
	return true, nil
}

func (s *SessionStore) Delete(id string) {
	if s == nil || id == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.sessions, id)
}

func (s *SessionStore) Cleanup(now time.Time) {
	if s == nil {
		return
	}
	if now.IsZero() {
		now = time.Now()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, session := range s.sessions {
		if !now.Before(session.ExpiresAt(s.ttl)) {
			delete(s.sessions, id)
		}
	}
}
