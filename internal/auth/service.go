package auth

// Service wraps the auth store and provides the business logic layer.
// Currently thin; exists to provide a consistent service interface across modules.
type Service struct {
	Store *Store
}

func NewService(store *Store) *Service {
	return &Service{Store: store}
}
