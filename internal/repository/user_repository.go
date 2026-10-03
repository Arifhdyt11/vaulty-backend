package repository

import (
	"context"

	"vaulty-api/internal/model"
	"vaulty-api/internal/repository/queries"
)

type UserRepository struct{ q *queries.Queries }

func NewUserRepository(q *queries.Queries) *UserRepository { return &UserRepository{q: q} }

type NewUser struct {
	Email, Name, PasswordHash, GoogleSub, AvatarURL string
}

func (r *UserRepository) Create(ctx context.Context, u NewUser) (model.User, error) {
	row, err := r.q.CreateUser(ctx, queries.CreateUserParams{
		Email:        u.Email,
		Name:         u.Name,
		PasswordHash: text(u.PasswordHash),
		GoogleSub:    text(u.GoogleSub),
		AvatarUrl:    text(u.AvatarURL),
	})
	if isUniqueViolation(err) {
		return model.User{}, model.ErrEmailTaken
	}
	return toUser(row), err
}

func (r *UserRepository) FindByID(ctx context.Context, id int64) (model.User, error) {
	row, err := r.q.GetUserByID(ctx, id)
	return toUser(row), notFound(err)
}

// FindByEmail juga mengembalikan hash password ("" untuk akun Google-only).
func (r *UserRepository) FindByEmail(ctx context.Context, email string) (model.User, string, error) {
	row, err := r.q.GetUserByEmail(ctx, email)
	return toUser(row), row.PasswordHash.String, notFound(err)
}

func (r *UserRepository) FindByGoogleSub(ctx context.Context, sub string) (model.User, error) {
	row, err := r.q.GetUserByGoogleSub(ctx, text(sub))
	return toUser(row), notFound(err)
}

// LinkGoogle menautkan akun Google ke akun yang sudah ada (avatar & nama hanya diisi jika kosong).
func (r *UserRepository) LinkGoogle(ctx context.Context, userID int64, sub, avatarURL, name string) (model.User, error) {
	row, err := r.q.LinkGoogleAccount(ctx, queries.LinkGoogleAccountParams{
		ID:        userID,
		GoogleSub: text(sub),
		AvatarUrl: text(avatarURL),
		Name:      name,
	})
	return toUser(row), notFound(err)
}

func toUser(u queries.User) model.User {
	return model.User{
		ID:        u.ID,
		Email:     u.Email,
		Name:      u.Name,
		AvatarURL: u.AvatarUrl.String,
		Role:      u.Role,
		HasGoogle: u.GoogleSub.Valid,
		CreatedAt: u.CreatedAt.Time,
	}
}
