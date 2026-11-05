package app

func newBtopQuickEditor() *settingsEditor {
	e := newSettingsEditor(false)
	e.quick = true
	c := btopProcessOptions()
	if e.draft.Htop == nil {
		e.draft.Htop = &HtopConfig{}
	}
	e.draft.Htop.Processes = &c
	for _, s := range e.fields() {
		e.edits[s.Key] = configRawValue(e.draft, s.Key)
	}
	return e
}
