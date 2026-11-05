package service

func validateAPIKeyRouteSubscriptionIdentity(key *APIKey, user *User, sub *UserSubscription) error {
	if sub == nil || key == nil || !key.HasMultipleRoutes() {
		return nil
	}
	if key == nil || key.GroupID == nil || key.Group == nil || !key.Group.IsSubscriptionType() {
		return ErrSubscriptionInvalid
	}
	if (sub.GroupID != 0 && sub.GroupID != *key.GroupID) || (sub.UserID != 0 && key.UserID != 0 && sub.UserID != key.UserID) || (user != nil && sub.UserID != 0 && sub.UserID != user.ID) {
		return ErrSubscriptionInvalid
	}
	return nil
}
