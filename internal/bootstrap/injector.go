package bootstrap

import (
	"github.com/primandproper/platform-go/v15/authentication/signin"
	"github.com/primandproper/platform-go/v15/identity"
	"github.com/primandproper/primitives-go/v2/database"

	"github.com/samber/do/v2"
)

// FromInjector builds a Seeder from a composed injector, which is what the
// two command-line tools hold. The registrar is the real sign-in service, so
// a seeded owner is registered exactly as a wire registration would be.
func FromInjector(i do.Injector) (*Seeder, error) {
	registrar, err := do.Invoke[*signin.Service](i)
	if err != nil {
		return nil, err
	}

	directory, err := do.Invoke[*identity.Service](i)
	if err != nil {
		return nil, err
	}

	users, err := do.Invoke[identity.Store](i)
	if err != nil {
		return nil, err
	}

	client, err := do.Invoke[database.Client](i)
	if err != nil {
		return nil, err
	}

	return New(registrar, directory, users, client.Reader())
}
