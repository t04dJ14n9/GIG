// goroutine.go implements *ssa.Go, *ssa.Send, *ssa.Select.
// Goroutines spawn the callee in a new goroutine via callSSA. Send
// uses reflect.Value.Send. Select builds reflect.SelectCase entries
// and dispatches via reflect.Select.
package interp

import (
	"context"
	"fmt"
	"reflect"

	"go/types"

	"golang.org/x/tools/go/ssa"

	"github.com/t04dJ14n9/gig/value"
)

func (p *program) runGo(fr *frame, instr *ssa.Go) (continuation, []value.Value, error) {
	common := instr.Common()
	args, err := p.readValuesInto(fr, common.Args, nil)
	if err != nil {
		return contNext, nil, err
	}
	switch tgt := common.Value.(type) {
	case *ssa.Function:
		ctx := fr.ctx
		go func() {
			defer func() { _ = recover() }()
			_, _ = p.callStaticFunction(ctx, nil, tgt, args, 0)
		}()
		return contNext, nil, nil
	case *ssa.Builtin:
		go func() {
			defer func() { _ = recover() }()
			_, _ = p.executeBuiltin(nil, tgt, args)
		}()
		return contNext, nil, nil
	}
	// Indirect (closure/function value).
	target, err := p.readValue(fr, common.Value)
	if err != nil {
		return contNext, nil, err
	}
	rv, err := p.reflectOf(target, nil)
	if err != nil {
		return contNext, nil, err
	}
	if rv.Kind() != reflect.Func {
		return contNext, nil, fmt.Errorf("interp: go target not callable (kind=%s)", rv.Kind())
	}
	rargs, err := p.reflectArgs(rv.Type(), args)
	if err != nil {
		return contNext, nil, err
	}
	go func() {
		defer func() { _ = recover() }()
		rv.Call(rargs)
	}()
	return contNext, nil, nil
}

func (p *program) runSend(fr *frame, instr *ssa.Send) (continuation, []value.Value, error) {
	chV, err := p.readValue(fr, instr.Chan)
	if err != nil {
		return contNext, nil, err
	}
	xV, err := p.readValue(fr, instr.X)
	if err != nil {
		return contNext, nil, err
	}
	rv, err := p.reflectOf(chV, nil)
	if err != nil {
		return contNext, nil, err
	}
	for rv.Kind() == reflect.Interface {
		rv = rv.Elem()
	}
	rx, err := p.reflectOf(xV, rv.Type().Elem())
	if err != nil {
		return contNext, nil, err
	}
	if err := sendWithContext(fr.ctx, rv, rx); err != nil {
		return contNext, nil, err
	}
	return contNext, nil, nil
}

func (p *program) runSelect(fr *frame, instr *ssa.Select) (continuation, []value.Value, error) {
	if fr.ctx != nil {
		if err := fr.ctx.Err(); err != nil {
			return contNext, nil, err
		}
	}

	cases := make([]reflect.SelectCase, 0, len(instr.States)+1)
	if !instr.Blocking {
		cases = append(cases, reflect.SelectCase{Dir: reflect.SelectDefault})
	}
	for _, st := range instr.States {
		ch, err := p.readValue(fr, st.Chan)
		if err != nil {
			return contNext, nil, err
		}
		chRV, err := p.reflectOf(ch, nil)
		if err != nil {
			return contNext, nil, err
		}
		for chRV.Kind() == reflect.Interface {
			chRV = chRV.Elem()
		}
		switch st.Dir {
		case types.SendOnly:
			val, err := p.readValue(fr, st.Send)
			if err != nil {
				return contNext, nil, err
			}
			vRV, err := p.reflectOf(val, chRV.Type().Elem())
			if err != nil {
				return contNext, nil, err
			}
			cases = append(cases, reflect.SelectCase{
				Dir:  reflect.SelectSend,
				Chan: chRV,
				Send: vRV,
			})
		case types.RecvOnly:
			cases = append(cases, reflect.SelectCase{
				Dir:  reflect.SelectRecv,
				Chan: chRV,
			})
		}
	}
	cancelCase := -1
	if instr.Blocking && fr.ctx != nil && fr.ctx.Done() != nil {
		cancelCase = len(cases)
		cases = append(cases, reflect.SelectCase{
			Dir:  reflect.SelectRecv,
			Chan: reflect.ValueOf(fr.ctx.Done()),
		})
	}
	chosen, recv, recvOK := reflect.Select(cases)
	if chosen == cancelCase {
		return contNext, nil, fr.ctx.Err()
	}
	if !instr.Blocking {
		chosen-- // default has index -1 in SSA terms
	}

	tt, ok := instr.Type().(*types.Tuple)
	if !ok {
		return contNext, nil, fmt.Errorf("interp: Select type is not tuple")
	}
	rt, err := p.resolver.ResolveType(tt)
	if err != nil {
		return contNext, nil, err
	}
	holder := reflect.New(rt).Elem()
	holder.Field(0).SetInt(int64(chosen))
	holder.Field(1).SetBool(recvOK)
	// Recv result fields (one per RecvOnly state) follow.
	recvFieldIdx := 2
	for i, st := range instr.States {
		if st.Dir != types.RecvOnly {
			continue
		}
		f := holder.Field(recvFieldIdx)
		if int(i) == chosen && recvOK {
			if f.Kind() == reflect.Interface {
				f.Set(recv)
			} else if recv.Type() != f.Type() && recv.Type().ConvertibleTo(f.Type()) {
				f.Set(recv.Convert(f.Type()))
			} else {
				f.Set(recv)
			}
		}
		recvFieldIdx++
	}
	fr.setValue(instr, reflectValue(holder))
	return contNext, nil, nil
}

func sendWithContext(ctx context.Context, channel, send reflect.Value) error {
	if ctx == nil || ctx.Done() == nil {
		channel.Send(send)
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	chosen, _, _ := reflect.Select([]reflect.SelectCase{
		{Dir: reflect.SelectSend, Chan: channel, Send: send},
		{Dir: reflect.SelectRecv, Chan: reflect.ValueOf(ctx.Done())},
	})
	if chosen == 1 {
		return ctx.Err()
	}
	return nil
}

func recvWithContext(ctx context.Context, channel reflect.Value) (reflect.Value, bool, error) {
	if ctx == nil || ctx.Done() == nil {
		received, ok := channel.Recv()
		return received, ok, nil
	}
	if err := ctx.Err(); err != nil {
		return reflect.Value{}, false, err
	}

	chosen, received, ok := reflect.Select([]reflect.SelectCase{
		{Dir: reflect.SelectRecv, Chan: channel},
		{Dir: reflect.SelectRecv, Chan: reflect.ValueOf(ctx.Done())},
	})
	if chosen == 1 {
		return reflect.Value{}, false, ctx.Err()
	}
	return received, ok, nil
}
