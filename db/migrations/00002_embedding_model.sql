-- +goose Up
-- Model pembuat embedding. Vector dari model berbeda tidak bisa dibandingkan, jadi search
-- hanya memakai embedding dari model aktif dan worker meng-embed ulang sisanya.
ALTER TABLE notes ADD COLUMN embedding_model TEXT;

-- +goose Down
ALTER TABLE notes DROP COLUMN embedding_model;
