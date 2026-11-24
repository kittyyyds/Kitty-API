package service

func (s *BillingService) SetGlobalContextRules(store *GlobalContextRuleStore) {
	if s != nil {
		s.globalContextRules = store
	}
}
