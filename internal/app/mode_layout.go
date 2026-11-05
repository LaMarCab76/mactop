package app

type modeLayoutState struct {
	Current, Last, Previous, PreviousStable string
	Temporary, PreviousTemporary            bool
}

var modeLayouts = map[string]modeLayoutState{}
var runtimeLayout string
var previousStableLayout string

func resetModeLayouts() {
	modeLayouts = make(map[string]modeLayoutState)
	runtimeLayout, lastActiveLayout, previousLayout, previousStableLayout = "", "", "", ""
	temporaryLayout, previousLayoutTemporary = false, false
}

func activeLayoutOrder() []string {
	if isHtopMode() {
		order := []string{LayoutDefault, LayoutGPUFocus, LayoutEnergy, LayoutProcesses, LayoutTop3}
		for _, layout := range layoutOrder {
			if !stringIn(layout, order) {
				order = append(order, layout)
			}
		}
		return order
	}
	return layoutOrder
}

func preferredLayout() string {
	if isHtopMode() {
		return htopConfig().Layout
	}
	if currentConfig.DefaultLayout != "" {
		return currentConfig.DefaultLayout
	}
	return LayoutDefault
}

func effectiveLayout() string {
	if runtimeLayout != "" {
		return runtimeLayout
	}
	return preferredLayout()
}

func persistLayoutPreference(layout string) {
	if isHtopMode() {
		if currentConfig.Htop == nil {
			currentConfig.Htop = &HtopConfig{}
		}
		currentConfig.Htop.Layout = layout
	} else {
		currentConfig.DefaultLayout = layout
	}
}

func changeModeLayout(mode string) {
	modeLayouts[activeUIMode] = modeLayoutState{Current: effectiveLayout(), Last: lastActiveLayout, Previous: previousLayout, PreviousStable: previousStableLayout, Temporary: temporaryLayout, PreviousTemporary: previousLayoutTemporary}
	activeUIMode = mode
	state, ok := modeLayouts[mode]
	if !ok {
		state = modeLayoutState{Current: preferredLayout(), Last: preferredLayout()}
	}
	runtimeLayout, lastActiveLayout, previousLayout, previousStableLayout = state.Current, state.Last, state.Previous, state.PreviousStable
	temporaryLayout, previousLayoutTemporary = state.Temporary, state.PreviousTemporary
	updateLayoutNumber()
}

func updateLayoutNumber() {
	currentLayoutNum, totalLayouts = 0, len(activeLayoutOrder())
	for i, layout := range activeLayoutOrder() {
		if layout == effectiveLayout() {
			currentLayoutNum = i
			break
		}
	}
}
