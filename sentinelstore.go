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
	"strconv"
)


// 会话序列化接口
type SessionSerializer interface {
	Deserialize(d []byte, ss *sessions.Session) error
	Serialize(ss *sessions.Session) ([]byte, error)
}

// json 序列化类型
type JSONSerializer struct{}

// json 序列化
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

// json 反序列化
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

// Gob 序列化类型
type GobSerializer struct{}

// Gob 序列化
func (s GobSerializer) Serialize(ss *sessions.Session) ([]byte, error) {
	buf := new(bytes.Buffer)
	enc := gob.NewEncoder(buf)
	err := enc.Encode(ss.Values)
	if err == nil {
		return buf.Bytes(), nil
	}
	return nil, err
}

// Gob 反序列化
func (s GobSerializer) Deserialize(d []byte, ss *sessions.Session) error {
	dec := gob.NewDecoder(bytes.NewBuffer(d))
	return dec.Decode(&ss.Values)
}

// SentinleStore类型
type SentinleStore struct {
	Sentinel          *radix.Sentinel
	Codecs        []securecookie.Codec
	Options       *sessions.Options
	DefaultMaxAge int
	maxLength     int
	keyPrefix     string
	serializer    SessionSerializer
}

// 设置允许的最大长度 默认4096
func (s *SentinleStore) SetMaxLength(l int) {
	if l >= 0 {
		s.maxLength = l
	}
}

// 设置prefix
func (s *SentinleStore) SetKeyPrefix(p string) {
	s.keyPrefix = p
}

// 设置序列化工具
func (s *SentinleStore) SetSerializer(ss SessionSerializer) {
	s.serializer = ss
}

// 设置最大的cookie过期时间
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


// sentinel相关配置

var TimeOut = 300
var RedisPassWord = ""
var RedisDB = 0
var RedisPoolNum = 100
var RedisMasterName = "mymaster"
var Sentinels = []string{}

// senintel 连接函数
func sentinelConnFunc(network,addr string) (radix.Conn,error)  {
	conn,err := radix.DialTimeout(network,addr,time.Millisecond * time.Duration(TimeOut))
	return conn,err
}
// redis 连接函数
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


// redis 连接池 函数
func redisPoolFunc(network, addr string) (radix.Client, error) {
	return radix.NewPool(network, addr, RedisPoolNum, radix.PoolConnFunc(redisConnFunc))
}

// 创建sentine store
func NewSentinelStore(sentinels []string, mastername, password string,poolsize,timeout,sessionExpire int, keyPairs ...[]byte) (*SentinleStore, error) {
	Sentinels = sentinels
	RedisMasterName = mastername
	RedisPassWord = password
	RedisPoolNum = poolsize
	TimeOut = timeout


	SentinelRedis,SRError:=radix.NewSentinel(RedisMasterName,Sentinels,radix.SentinelConnFunc(sentinelConnFunc),radix.SentinelPoolFunc(redisPoolFunc))
	rs := &SentinleStore{
		Sentinel:   SentinelRedis,
		Codecs: securecookie.CodecsFromPairs(keyPairs...),
		Options: &sessions.Options{
			Path:   "/",
			MaxAge: sessionExpire,
		},
		DefaultMaxAge: 60 * 20, // 默认MaxAge
		maxLength:     4096,
		keyPrefix:     "session_",
		serializer:    GobSerializer{},
	}
	return rs, SRError

}


// Close sentinel 连接
func (s *SentinleStore) Close() error {
	return s.Sentinel.Close()
}

// 获取session 实例
//
// See gorilla/sessions FilesystemStore.Get().
func (s *SentinleStore) Get(r *http.Request, name string) (*sessions.Session, error) {
	return sessions.GetRegistry(r).Get(s, name)
}

// 返回一个新session实例
//
// See gorilla/sessions FilesystemStore.New().
func (s *SentinleStore) New(r *http.Request, name string) (*sessions.Session, error) {
	var (
		err error
		ok  bool
	)
	session := sessions.NewSession(s, name)
	options := *s.Options
	session.Options = &options
	session.IsNew = true
	if c, errCookie := r.Cookie(name); errCookie == nil {
		err = securecookie.DecodeMulti(name, c.Value, &session.ID, s.Codecs...)
		if err == nil {
			ok, err = s.load(session)
			session.IsNew = !(err == nil && ok)
		}
	}
	return session, err
}

// 保存session信息
func (s *SentinleStore) Save(r *http.Request, w http.ResponseWriter, session *sessions.Session) error {

	if session.Options.MaxAge < 0 {
		if err := s.delete(session); err != nil {
			return err
		}
		http.SetCookie(w, sessions.NewCookie(session.Name(), "", session.Options))
	} else {
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

// 删除session信息
func (s *SentinleStore) Delete(r *http.Request, w http.ResponseWriter, session *sessions.Session) error {

	if err := s.Sentinel.Do(radix.Cmd(nil,"DEL",s.keyPrefix+session.ID)); err != nil {
		return err
	}
	// 设置cookie过期
	options := *session.Options
	options.MaxAge = -1
	http.SetCookie(w, sessions.NewCookie(session.Name(), "", &options))
	// 删除session 信息
	for k := range session.Values {
		delete(session.Values, k)
	}
	return nil
}

// 实际保存seesion操作.
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

// load 获取到的session信息
func (s *SentinleStore) load(session *sessions.Session) (bool, error) {
	var data []byte
	if err := s.Sentinel.Do(radix.Cmd(&data,"GET",s.keyPrefix+session.ID)); err != nil {
		return false,err
	}
	return true, s.serializer.Deserialize(data, session)
}

// 删除redis上的session key
func (s *SentinleStore) delete(session *sessions.Session) error {
	return s.Sentinel.Do(radix.Cmd(nil,"DEL", s.keyPrefix+session.ID))
}
