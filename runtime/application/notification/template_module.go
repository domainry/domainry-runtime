package notification

import (
	sourcenotification "github.com/domainry/domainry-notification"
	sourcetemplate "github.com/domainry/domainry-notification/template"
	notificationcontract "github.com/domainry/domainry-runtime/runtime/domain/notification/contract"
	notificationmodel "github.com/domainry/domainry-runtime/runtime/domain/notification/model"
)

type TemplateModuleDependencies struct {
	Store        sourcetemplate.Store
	Clock        sourcenotification.Clock
	WorkerID     string
	Directory    sourcetemplate.RecipientDirectory
	WorkNotifier sourcetemplate.PublicationWorkNotifier
}

func NewTemplateModule(defaultLocale string, installed []notificationmodel.NotificationTemplate, dependencies TemplateModuleDependencies) (*sourcetemplate.Engine, *sourcetemplate.Manager, *sourcetemplate.PublicationProcessor, error) {
	capabilities, err := notificationcontract.NotificationTemplateCapabilityCatalog()
	if err != nil {
		return nil, nil, nil, err
	}
	validator, err := sourcetemplate.NewValidator(capabilities)
	if err != nil {
		return nil, nil, nil, err
	}
	values := make([]sourcetemplate.Template, len(installed))
	for i, v := range installed {
		values[i] = moduleTemplate(v)
	}
	engine, err := sourcetemplate.NewEngine(defaultLocale, values, validator, dependencies.Directory)
	if err != nil {
		return nil, nil, nil, err
	}
	manager, err := sourcetemplate.NewManager(sourcetemplate.ManagerDependencies{Store: dependencies.Store, Engine: engine, Validator: validator})
	if err != nil {
		return nil, nil, nil, err
	}
	processor, err := sourcetemplate.NewPublicationProcessor(sourcetemplate.PublicationProcessorDependencies{Store: dependencies.Store, Manager: manager, Clock: dependencies.Clock, WorkerID: dependencies.WorkerID, WorkNotifier: dependencies.WorkNotifier})
	if err != nil {
		return nil, nil, nil, err
	}
	return engine, manager, processor, nil
}
