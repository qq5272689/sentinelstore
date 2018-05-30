package sentinelstore

import (
	nSessions "github.com/goincremental/negroni-sessions"
	gSessions "github.com/gorilla/sessions"
)

//New returns a new Sentinel store
func NewNegroniSentinelStore(sentinels []string, mastername, password string,poolsize,timeout,sessionExpire int, keyPairs ...[]byte) (nSessions.Store, error) {
	store, err := NewSentinelStore(sentinels , mastername, password ,poolsize,timeout,sessionExpire, keyPairs...)
	if err != nil {
		return nil, err
	}
	return &NegroniSentinleStore{store}, nil
}

type NegroniSentinleStore struct {
	*SentinleStore
}

func (c *NegroniSentinleStore) Options(options nSessions.Options) {
	c.SentinleStore.Options = &gSessions.Options{
		Path:     options.Path,
		Domain:   options.Domain,
		MaxAge:   options.MaxAge,
		Secure:   options.Secure,
		HttpOnly: options.HTTPOnly,
	}
}

