package email

type Service struct {
	Store      *Store
	SMTPServer *SMTPServer
	Client     *SMTPClient
}

func NewService(store *Store, domain, smtpAddr string) *Service {
	return &Service{
		Store:      store,
		SMTPServer: NewSMTPServer(store, domain, smtpAddr),
		Client:     NewSMTPClient(store, domain),
	}
}
