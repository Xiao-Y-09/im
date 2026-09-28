package service

import (
	"errors"
	"regexp"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/bcrypt"

	"im/internal/dao"
	"im/internal/model"
)

const tokenTTL = 7 * 24 * time.Hour

var usernameRe = regexp.MustCompile(`^[A-Za-z0-9_]{3,32}$`)

type UserService struct {
	users  *dao.UserDAO
	secret []byte
}

func NewUserService(users *dao.UserDAO, jwtSecret string) *UserService {
	return &UserService{users: users, secret: []byte(jwtSecret)}
}

type UserView struct {
	ID       int64  `json:"id"`
	Username string `json:"username"`
}

func viewOf(u *model.User) UserView {
	return UserView{ID: u.ID, Username: u.Username}
}

func (s *UserService) Register(username, password string) (UserView, error) {
	if !usernameRe.MatchString(username) {
		return UserView{}, newErr(CodeBadRequest, "username must be 3-32 letters, digits or _")
	}
	if len(password) < 6 || len(password) > 72 {
		return UserView{}, newErr(CodeBadRequest, "password must be 6-72 characters")
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return UserView{}, internalErr(err)
	}
	u := &model.User{Username: username, PwdHash: hash}
	if err := s.users.Create(u); err != nil {
		if errors.Is(err, dao.ErrDuplicate) {
			return UserView{}, newErr(CodeConflict, "username already taken")
		}
		return UserView{}, internalErr(err)
	}
	return viewOf(u), nil
}

func (s *UserService) Login(username, password string) (string, UserView, error) {
	u, err := s.users.GetByUsername(username)
	if err != nil {
		if errors.Is(err, dao.ErrNotFound) {
			return "", UserView{}, newErr(CodeUnauthorized, "wrong username or password")
		}
		return "", UserView{}, internalErr(err)
	}
	if bcrypt.CompareHashAndPassword(u.PwdHash, []byte(password)) != nil {
		return "", UserView{}, newErr(CodeUnauthorized, "wrong username or password")
	}
	tok, err := s.sign(u.ID)
	if err != nil {
		return "", UserView{}, internalErr(err)
	}
	return tok, viewOf(u), nil
}

type claims struct {
	UID int64 `json:"uid"`
	jwt.RegisteredClaims
}

func (s *UserService) sign(uid int64) (string, error) {
	now := time.Now()
	c := claims{
		UID: uid,
		RegisteredClaims: jwt.RegisteredClaims{
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(tokenTTL)),
		},
	}
	return jwt.NewWithClaims(jwt.SigningMethodHS256, c).SignedString(s.secret)
}

func (s *UserService) ParseToken(tok string) (int64, error) {
	var c claims
	_, err := jwt.ParseWithClaims(tok, &c, func(*jwt.Token) (any, error) {
		return s.secret, nil
	}, jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}), jwt.WithExpirationRequired())
	if err != nil || c.UID <= 0 {
		return 0, newErr(CodeUnauthorized, "invalid or expired token")
	}
	return c.UID, nil
}

func (s *UserService) Me(uid int64) (UserView, error) {
	u, err := s.users.GetByID(uid)
	if err != nil {
		if errors.Is(err, dao.ErrNotFound) {
			return UserView{}, newErr(CodeUnauthorized, "user no longer exists")
		}
		return UserView{}, internalErr(err)
	}
	return viewOf(u), nil
}

func (s *UserService) Search(q string) ([]UserView, error) {
	if q == "" {
		return []UserView{}, nil
	}
	us, err := s.users.SearchPrefix(q, 20)
	if err != nil {
		return nil, internalErr(err)
	}
	return views(us), nil
}

func (s *UserService) ByIDs(ids []int64) ([]UserView, error) {
	if len(ids) > 200 {
		return nil, newErr(CodeBadRequest, "at most 200 ids")
	}
	us, err := s.users.GetByIDs(ids)
	if err != nil {
		return nil, internalErr(err)
	}
	return views(us), nil
}

func views(us []model.User) []UserView {
	out := make([]UserView, 0, len(us))
	for i := range us {
		out = append(out, viewOf(&us[i]))
	}
	return out
}
