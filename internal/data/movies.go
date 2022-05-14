package data

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"github.com/lib/pq"
	"greenlight.sajjadanwar.io/internal/validator"
	"time"
)

type MovieModel struct {
	DB *sql.DB
}
type Movie struct {
	ID        int64     `json:"id"`
	CreatedAt time.Time `json:"-"`
	Title     string    `json:"title"`
	Year      int32     `json:"year,omitempty"`
	Runtime   Runtime   `json:"runtime,omitempty"`
	Genres    []string  `json:"genres,omitempty"`
	Version   int32     `json:"version"`
}

func (m MovieModel) Insert(movie *Movie) error {
	query := `
INSERT INTO movies (title, year, runtime, genres)
VALUES ($1, $2, $3, $4)
RETURNING id, created_at, version`
	args := []interface{}{movie.Title, movie.Year, movie.Runtime, pq.Array(movie.Genres)}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	// Use QueryRowContext() and pass the context as the first argument.
	return m.DB.QueryRowContext(ctx, query, args...).Scan(&movie.ID, &movie.CreatedAt, &movie.Version)
}
func (m MovieModel) Get(id int64) (*Movie, error) {
	if id < 1 {
		return nil, ErrRecordNotFound
	}
	query := `
SELECT  id, created_at, title, year, runtime, genres, version
FROM movies
WHERE id = $1`
	var movie Movie
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	err := m.DB.QueryRowContext(ctx, query, id).Scan(
		&movie.ID,
		&movie.CreatedAt,
		&movie.Title,
		&movie.Year,
		&movie.Runtime,
		pq.Array(&movie.Genres),
		&movie.Version,
	)

	if err != nil {
		switch {
		case errors.Is(err, sql.ErrNoRows):
			return nil, ErrRecordNotFound
		default:
			return nil, err
		}

	}

	return &movie, nil
}
func (m MovieModel) Update(movie *Movie) error {
	query := `
UPDATE movies
SET title = $1, year = $2, runtime = $3, genres = $4, version = version + 1
WHERE id = $5 AND version = $6
RETURNING version`
	// Create an args slice containing the values for the placeholder parameters.
	args := []interface{}{
		movie.Title,
		movie.Year,
		movie.Runtime,
		pq.Array(movie.Genres),
		movie.ID,
		movie.Version,
	}

	// Use the QueryRow() method to execute the query, passing in the args slice as a
	// variadic parameter and scanning the new version value into the movie struct.
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	err := m.DB.QueryRowContext(ctx, query, args...).Scan(&movie.Version)
	if err != nil {
		switch {
		case errors.Is(err, sql.ErrNoRows):
			return ErrEditConflict
		default:
			return err
		}
	}
	return nil
}
func (m MovieModel) Delete(id int64) error {
	if id < 1 {
		return ErrRecordNotFound
	}
	query := `
DELETE FROM movies
WHERE id = $1`

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	// Use ExecContext() and pass the context as the first argument.
	result, err := m.DB.ExecContext(ctx, query, id)
	if err != nil {
		return err
	}
	rowsEffected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rowsEffected == 0 {
		return ErrRecordNotFound
	}
	return nil
}

func ValidateMovie(v *validator.Validator, movie *Movie) {
	v.Check(movie.Title != "", "title", "must be provided")
	v.Check(len(movie.Title) <= 500, "title", "must not be more than 500 bytes long")
	v.Check(movie.Year != 0, "year", "must be provided")
	v.Check(movie.Year >= 1888, "year", "must be greater than 1888")
	v.Check(movie.Year <= int32(time.Now().Year()), "year", "must not be in the future")
	v.Check(movie.Runtime != 0, "runtime", "must be provided")
	v.Check(movie.Runtime > 0, "runtime", "must be a positive integer")
	v.Check(movie.Genres != nil, "genres", "must be provided")
	v.Check(len(movie.Genres) >= 1, "genres", "must contain at least 1 genre")
	v.Check(len(movie.Genres) <= 5, "genres", "must not contain more than 5 genres")
	v.Check(validator.Unique(movie.Genres), "genres", "must not contain duplicate values")
}
func (m MovieModel) GetAll(title string, genres []string, filters Filters) ([]*Movie, Metadata, error) {
	// Add an ORDER BY clause and interpolate the sort column and direction. Importantly
	// notice that we also include a secondary sort on the movie ID to ensure a
	// consistent ordering.
	query := fmt.Sprintf(`
SELECT count(*) OVER(), id, created_at, title, year, runtime, genres, version
FROM movies
WHERE (to_tsvector('simple', title) @@ plainto_tsquery('simple', $1) OR $1 = '')
AND (genres @> $2 OR $2 = '{}')
ORDER BY %s %s, id ASC
LIMIT $3 OFFSET $4`, filters.sortColumn(), filters.sortDirection())

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	// As our SQL query now has quite a few placeholder parameters, let's collect the
	// values for the placeholders in a slice. Notice here how we call the limit() and
	// offset() methods on the Filters' struct to get the appropriate values for the
	// LIMIT and OFFSET clauses.
	args := []interface{}{title, pq.Array(genres), filters.limit(), filters.offset()}
	rows, err := m.DB.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, Metadata{}, err // Update this to return an empty Metadata struct.
	}
	// Importantly, defer a call to rows.Close() to ensure that the resultset is closed
	// before GetAll() returns.
	defer rows.Close()
	totalRecords := 0
	// Initialize an empty slice to hold the movie data.
	movies := []*Movie{}
	// Use rows.Next to iterate through the rows in the resultset.
	for rows.Next() {
		// Initialize an empty Movie struct to hold the data for an individual movie.
		var movie Movie
		// Scan the values from the row into the Movie struct. Again, note that we're
		// using the pq.Array() adapter on the genres field here.
		err := rows.Scan(
			&totalRecords,
			&movie.ID,
			&movie.CreatedAt,
			&movie.Title,
			&movie.Year,
			&movie.Runtime,
			pq.Array(&movie.Genres),
			&movie.Version,
		)
		if err != nil {
			return nil, Metadata{}, err // Update this to return an empty Metadata struct.
		}
		movies = append(movies, &movie)
	}
	// When the rows.Next() loop has finished, call rows.Err() to retrieve any error
	// that was encountered during the iteration.
	if err = rows.Err(); err != nil {
		return nil, Metadata{}, err // Update this to return an empty Metadata struct.

	}
	// Generate a Metadata struct, passing in the total record count and pagination
	// parameters from the client.
	metadata := calculateMetadata(totalRecords, filters.Page, filters.PageSize)
	// If everything went OK, then return the slice of movies.
	return movies, metadata, nil
}
