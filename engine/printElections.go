package engine

import (
	"fmt"
	"github.com/FactomProject/factomd/common/interfaces"
	"github.com/FactomProject/factomd/elections"
	"github.com/FactomProject/factomd/state"
	"time"
)

func lookup(id interfaces.IHash) *state.State {
	for _, fn := range fnodes {
		if fn.State.IdentityChainID.Fixed() == id.Fixed() {
			return fn.State
		}
	}
	return nil
}

func printElections(elects *int, value int, listenTo *int, wsapiNode *int) {
	out := ""

	if *listenTo < 0 || *listenTo >= len(fnodes) {
		return
	}

	for *elects == value {
		prt := "===ElectionsStart===\n\n"
		if len(fnodes) == 0 {
			return
		}

		s := fnodes[*listenTo].State
		eo := s.Elections.(*elections.Elections)

		prt = prt + fmt.Sprintf("%3s %15s %15s\n", "#", "Federated", "Audit")
		for i := 0; i < len(eo.Federated)+len(eo.Audit); i++ {
			fed := ""
			aud := ""
			if i < len(eo.Federated) {
				id := eo.Federated[i].GetChainID()
				fed = lookup(id).FactomNodeName
			}
			if i < len(eo.Audit) {
				id := eo.Audit[i].GetChainID()
				aud = lookup(id).FactomNodeName
			}
			if fed == "" && aud == "" {
				break
			}
			prt = prt + fmt.Sprintf("%3d %15s %15s\n", i, fed, aud)
		}

		prt = prt + "\n" + fnodes[0].State.Election0
		for i, _ := range eo.Federated {
			prt = prt + fmt.Sprintf("%4d ", i)
		}
		for i, _ := range eo.Audit {
			prt = prt + fmt.Sprintf("%4d ", i)
		}
		prt = prt + "\n"
		for _, fn := range fnodes {
			s := fn.State
			prt = prt + s.Election1 + s.Election2 + "\n"
		}
		prt = prt + "===ElectionsEnd===\n"

		if prt != out {
			fmt.Println(prt)
			out = prt
		}

		time.Sleep(time.Second)
	}
}