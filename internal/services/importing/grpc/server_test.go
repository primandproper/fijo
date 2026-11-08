package importinggrpc

import (
	"testing"

	"github.com/primandproper/fijo/internal/importing/acuity"

	"github.com/shoenig/test"
	"github.com/shoenig/test/must"
)

func TestReadable(t *testing.T) {
	t.Parallel()

	_, err := acuity.Plan(acuity.Input{Clients: "Email,Phone\nana@example.com,5125550100\n"})
	must.Error(t, err)

	test.Eq(t, "The client list has no name column we recognize. Its columns are Email, Phone.", readable(err))

	_, err = acuity.Plan(acuity.Input{Appointments: "\n,\n"})
	must.Error(t, err)

	test.Eq(t, "The appointments report is empty.", readable(err))
}
