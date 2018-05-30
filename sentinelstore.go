package sentinelstore

import (
	"bytes"
	"encoding/base32"
	"encoding/gob"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
	"github.com/mediocregopher/radix.v3"
	"github.com/gorilla/securecookie"
	"github.com/gorilla/sessions"
	"github.com/garyburd/redigo/redis"
	"strconv"
)

// Amount of time for cookies/redis keys to expire.
//var sessionExpire = 86400 * 30

// SessionSerializer provides an interface hook for alternative serializers
type SessionSerializer interface {
	Deserialize(d []byte, ss *sessions.Session) error
	Serialize(ss *sessions.Session) ([]byte, error)
}

// JSONSerializer encode the session map to JSON.
type JSONSerializer struct{}

// Serialize to JSON. Will err if there are unmarshalable key values
func (s JSONSerializer) Serialize(ss *sessions.Session) ([]byte, error) {
	m := make(map[string]interface{}, len(ss.Values))
	for k, v := range ss.Values {
		ks, ok := k.(string)
		if !ok {
			err := fmt.Errorf("Non-string key value, cannot serialize session to JSON: %v", k)
			fmt.Printf("redistore.JSONSerializer.serialize() Error: %v", err)
			return nil, err
		}
		m[ks] = v
	}
	return json.Marshal(m)
}

// Deserialize back to map[string]interface{}
func (s JSONSerializer) Deserialize(d []byte, ss *sessions.Session) error {
	m := make(map[string]interface{})
	err := json.Unmarshal(d, &m)
	if err != nil {
		fmt.Printf("redistore.JSONSerializer.deserialize() Error: %v", err)
		return err
	}
	for k, v := range m {
		ss.Values[k] = v
	}
	return nil
}

// GobSerializer uses gob package to encode the session map
type GobSerializer struct{}

// Serialize using gob
func (s GobSerializer) Serialize(ss *sessions.Session) ([]byte, error) {
	buf := new(bytes.Buffer)
	enc := gob.NewEncoder(buf)
	err := enc.Encode(ss.Values)
	if err == nil {
		return buf.Bytes(), nil
	}
	return nil, err
}

// Deserialize back to map[interface{}]interface{}
func (s GobSerializer) Deserialize(d []byte, ss *sessions.Session) error {
	dec := gob.NewDecoder(bytes.NewBuffer(d))
	return dec.Decode(&ss.Values)
}

// RediStore stores sessions in a redis backend.
type SentinleStore struct {
	Sentinel          *radix.Sentinel
	Codecs        []securecookie.Codec
	Options       *sessions.Options // default configuration
	DefaultMaxAge int               // default Redis TTL for a MaxAge == 0 session
	maxLength     int
	keyPrefix     string
	serializer    SessionSerializer
}

// SetMaxLength sets RediStore.maxLength if the `l` argument is greater or equal 0
// maxLength restricts the maximum length of new sessions to l.
// If l is 0 there is no limit to the size of a session, use with caution.
// The default for a new RediStore is 4096. Redis allows for max.
// value sizes of up to 512MB (http://redis.io/topics/data-types)
// Default: 4096,
func (s *SentinleStore) SetMaxLength(l int) {
	if l >= 0 {
		s.maxLength = l
	}
}

// SetKeyPrefix set the prefix
func (s *SentinleStore) SetKeyPrefix(p string) {
	s.keyPrefix = p
}

// SetSerializer sets the serializer
func (s *SentinleStore) SetSerializer(ss SessionSerializer) {
	s.serializer = ss
}

// SetMaxAge restricts the maximum age, in seconds, of the session record
// both in database and a browser. This is to change session storage configuration.
// If you want just to remove session use your session `s` object and change it's
// `Options.MaxAge` to -1, as specified in
//    http://godoc.org/github.com/gorilla/sessions#Options
//
// Default is the one provided by this package value - `sessionExpire`.
// Set it to 0 for no restriction.
// Because we use `MaxAge` also in SecureCookie crypting algorithm you should
// use this function to change `MaxAge` value.
func (s *SentinleStore) SetMaxAge(v int) {
	var c *securecookie.SecureCookie
	var ok bool
	s.Options.MaxAge = v
	for i := range s.Codecs {
		if c, ok = s.Codecs[i].(*securecookie.SecureCookie); ok {
			c.MaxAge(v)
		} else {
			fmt.Printf("Can't change MaxAge on codec %v\n", s.Codecs[i])
		}
	}
}

func dial(network, address, password string) (redis.Conn, error) {
	c, err := redis.Dial(network, address)
	if err != nil {
		return nil, err
	}
	if password != "" {
		if _, err := c.Do("AUTH", password); err != nil {
			c.Close()
			return nil, err
		}
	}
	return c, err
}

// NewRediStore returns a new RediStore.
// size: maximum number of idle connections.

var TimeOut = 300
var RedisPassWord = ""
var RedisDB = 0
var RedisPoolNum = 100
var RedisMasterName = "mymaster"
var Sentinels = []string{}

func sentinelConnFunc(network,addr string) (radix.Conn,error)  {
	conn,err := radix.DialTimeout(network,addr,time.Millisecond * time.Duration(TimeOut))
	return conn,err
}

func redisConnFunc(network,addr string) (radix.Conn, error)  {
	conn, err := radix.DialTimeout(network, addr, time.Millisecond*time.Duration(TimeOut))
	if err != nil {
		return nil, err
	}
	if len(RedisPassWord)>0{
		if err := conn.Do(radix.Cmd(nil,"AUTH", RedisPassWord)); err != nil {
			conn.Close()
			return nil, err
		}
	}
	if err := conn.Do(radix.Cmd(nil,"SELECT", strconv.Itoa(RedisDB))); err != nil {
		conn.Close()
		return nil, err
	}
	return conn, nil
}

func redisPoolFunc(network, addr string) (radix.Client, error) {
	return radix.NewPool(network, addr, RedisPoolNum, radix.PoolConnFunc(redisConnFunc))
}


func NewSentinelStore(sentinels []string, mastername, password string,poolsize,timeout,sessionExpire int, keyPairs ...[]byte) (*SentinleStore, error) {
	Sentinels = sentinels
	RedisMasterName = mastername
	RedisPassWord = password
	RedisPoolNum = poolsize
	TimeOut = timeout


	SentinelRedis,SRError:=radix.NewSentinel(RedisMasterName,Sentinels,radix.SentinelConnFunc(sentinelConnFunc),radix.SentinelPoolFunc(redisPoolFunc))
	rs := &SentinleStore{
		// http://godoc.org/github.com/garyburd/redigo/redis#Pool
		Sentinel:   SentinelRedis,
		Codecs: securecookie.CodecsFromPairs(keyPairs...),
		Options: &sessions.Options{
			Path:   "/",
			MaxAge: sessionExpire,
		},
		DefaultMaxAge: 60 * 20, // 20 minutes seems like a reasonable default
		maxLength:     4096,
		keyPrefix:     "session_",
		serializer:    GobSerializer{},
	}
	return rs, SRError

}


// Close closes the underlying *redis.Pool
func (s *SentinleStore) Close() error {
	return s.Sentinel.Close()
}

// Get returns a session for the given name after adding it to the registry.
//
// See gorilla/sessions FilesystemStore.Get().
func (s *SentinleStore) Get(r *http.Request, name string) (*sessions.Session, error) {
	return sessions.GetRegistry(r).Get(s, name)
}

// New returns a session for the given name without adding it to the registry.
//
// See gorilla/sessions FilesystemStore.New().
func (s *SentinleStore) New(r *http.Request, name string) (*sessions.Session, error) {
	var (
		err error
		ok  bool
	)
	session := sessions.NewSession(s, name)
	// make a copy
	options := *s.Options
	session.Options = &options
	session.IsNew = true
	if c, errCookie := r.Cookie(name); errCookie == nil {
		err = securecookie.DecodeMulti(name, c.Value, &session.ID, s.Codecs...)
		if err == nil {
			ok, err = s.load(session)
			session.IsNew = !(err == nil && ok) // not new if no error and data available
		}
	}
	return session, err
}

// Save adds a single session to the response.
func (s *SentinleStore) Save(r *http.Request, w http.ResponseWriter, session *sessions.Session) error {
	// Marked for deletion.
	if session.Options.MaxAge < 0 {
		if err := s.delete(session); err != nil {
			return err
		}
		http.SetCookie(w, sessions.NewCookie(session.Name(), "", session.Options))
	} else {
		// Build an alphanumeric key for the redis store.
		if session.ID == "" {
			session.ID = strings.TrimRight(base32.StdEncoding.EncodeToString(securecookie.GenerateRandomKey(32)), "=")
		}
		if err := s.save(session); err != nil {
			return err
		}
		encoded, err := securecookie.EncodeMulti(session.Name(), session.ID, s.Codecs...)
		if err != nil {
			return err
		}
		http.SetCookie(w, sessions.NewCookie(session.Name(), encoded, session.Options))
	}
	return nil
}

// Delete removes the session from redis, and sets the cookie to expire.
//
// WARNING: This method should be considered deprecated since it is not exposed via the gorilla/sessions interface.
// Set session.Options.MaxAge = -1 and call Save instead. - July 18th, 2013
func (s *SentinleStore) Delete(r *http.Request, w http.ResponseWriter, session *sessions.Session) error {

	if err := s.Sentinel.Do(radix.Cmd(nil,"DEL",s.keyPrefix+session.ID)); err != nil {
		return err
	}
	// Set cookie to expire.
	options := *session.Options
	options.MaxAge = -1
	http.SetCookie(w, sessions.NewCookie(session.Name(), "", &options))
	// Clear session values.
	for k := range session.Values {
		delete(session.Values, k)
	}
	return nil
}

// ping does an internal ping against a server to check if it is alive.
//func (s *SentinleStore) ping() (bool, error) {
//	conn := s.Sentinel.Do()
//	data, err := conn.Do("PING")
//	if err != nil || data == nil {
//		return false, err
//	}
//	return (data == "PONG"), nil
//}

// save stores the session in redis.
func (s *SentinleStore) save(session *sessions.Session) error {
	b, err := s.serializer.Serialize(session)
	if err != nil {
		return err
	}
	if s.maxLength != 0 && len(b) > s.maxLength {
		return errors.New("SessionStore: the value to store is too big")
	}
	age := session.Options.MaxAge
	if age == 0 {
		age = s.DefaultMaxAge
	}
	return s.Sentinel.Do(radix.Cmd(nil,"SETEX",s.keyPrefix+session.ID, strconv.Itoa(age), string(b)))
}

// load reads the session from redis.
// returns true if there is a sessoin data in DB
func (s *SentinleStore) load(session *sessions.Session) (bool, error) {
	var data []byte
	if err := s.Sentinel.Do(radix.Cmd(&data,"GET",s.keyPrefix+session.ID)); err != nil {
		return false,err
	}
	return true, s.serializer.Deserialize(data, session)
}

// delete removes keys from redis if MaxAge<0
func (s *SentinleStore) delete(session *sessions.Session) error {
	return s.Sentinel.Do(radix.Cmd(nil,"DEL", s.keyPrefix+session.ID))
}
