package mincopies

import (
	"errors"
	"fmt"
	"os"
	"sync"

	"scat"
	"scat/concur"
	"scat/procs"
	"scat/stores"
	"scat/stores/copies"
	"scat/stores/quota"
)

type minCopies struct {
	min    int
	qman   *quota.Man
	reg    *copies.Reg
	finish func() error
}

func New(min int, qman *quota.Man) (dynp procs.DynProcer, err error) {
	reg := copies.NewReg()
	ress := qman.Resources(0)
	ml := stores.MultiLister(listers(ress))
	err = ml.AddEntriesTo([]stores.LsEntryAdder{
		stores.QuotaEntryAdder{Qman: qman},
		stores.CopiesEntryAdder{Reg: reg},
	})
	dynp = &minCopies{
		min:    min,
		qman:   qman,
		reg:    reg,
		finish: finishFuncs(ress).FirstErr,
	}
	return
}

func calcDataUse(d scat.Data) (uint64, error) {
	sz, ok := d.(scat.Sizer)
	if !ok {
		return 0, errors.New("sized-data required for calculating data use")
	}
	return uint64(sz.Size()), nil
}

var shuffle = stores.ShuffleCopiers // var for tests

func (mc *minCopies) Procs(c *scat.Chunk) ([]procs.Proc, error) {
	copies := mc.reg.List(c.Hash())
	copies.Mu.Lock()
	dataUse, err := calcDataUse(c.Data())
	if err != nil {
		return nil, err
	}
	all := shuffle(mc.getCopiers(dataUse))
	ncopies := copies.Len()
	navail := len(all) - ncopies
	missing := mc.min - ncopies
	if missing < 0 {
		missing = 0
	}
	if missing > navail {
		return nil, errors.New(fmt.Sprintf(
			"missing copiers to meet min requirement:"+
				" min=%d copies=%d missing=%d avail=%d",
			mc.min, ncopies, missing, navail,
		))
	}
	elected := make([]stores.Copier, 0, missing)
	failover := make([]stores.Copier, 0, navail-missing)
	for _, cp := range all {
		if copies.Contains(cp) {
			continue
		}
		if len(elected) < missing {
			elected = append(elected, cp)
		} else {
			failover = append(failover, cp)
		}
	}
	wg := sync.WaitGroup{}
	wg.Add(len(elected))
	go func() {
		defer copies.Mu.Unlock()
		wg.Wait()
	}()
	cpProcs := make([]procs.Proc, len(elected)+1)
	cpProcs[0] = procs.Nop
	copierProc := func(copier stores.Copier) procs.Proc {
		copiers := append([]stores.Copier{copier}, failover...)
		casc := make(procs.Cascade, len(copiers))
		for i := range copiers {
			cp := copiers[i]
			casc[i] = procs.OnEnd{cp, func(err error) {
				if err != nil {
					fmt.Fprintf(os.Stderr, "mincopies: copier error: %v\n", err)
					mc.qman.Delete(cp)
					return
				}
				copies.Add(cp)
				mc.qman.AddUse(cp, dataUse)
			}}
		}
		proc := procs.DiscardChunks{casc}
		return procs.OnEnd{proc, func(error) { wg.Done() }}
	}
	for i, copier := range elected {
		cpProcs[i+1] = copierProc(copier)
	}
	return cpProcs, nil
}

func (mc *minCopies) getCopiers(use uint64) (cps []stores.Copier) {
	ress := mc.qman.Resources(use)
	cps = make([]stores.Copier, len(ress))
	for i, res := range ress {
		cps[i] = res.(stores.Copier)
	}
	return
}

func (mc *minCopies) Finish() error {
	return mc.finish()
}

func listers(ress []quota.Res) (lsers []stores.Lister) {
	lsers = make([]stores.Lister, len(ress))
	for i, res := range ress {
		lsers[i] = res.(stores.Lister)
	}
	return
}

func finishFuncs(ress []quota.Res) (fns concur.Funcs) {
	fns = make(concur.Funcs, len(ress))
	for i, res := range ress {
		fns[i] = res.(procs.Proc).Finish
	}
	return
}
