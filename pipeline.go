package main

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// AgentConfig defines an AI agent for a pipeline stage.
type AgentConfig struct {
	Prompt       string   `yaml:"prompt"`
	AllowedTools []string `yaml:"allowedTools"`
}

// StageConfig defines a single stage in the pipeline.
type StageConfig struct {
	Name      string       `yaml:"name"`
	Title     string       `yaml:"title"`
	Agent     *AgentConfig `yaml:"agent"`
	Auto      bool         `yaml:"auto"`
	CanReject bool         `yaml:"canReject"`
	RejectTo  string       `yaml:"rejectTo"`
	Worktree  string       `yaml:"worktree"`
	Setup     string       `yaml:"setup"`
}

// PipelineConfig is the top-level pipeline configuration loaded from YAML.
type PipelineConfig struct {
	CreateTicket string        `yaml:"createTicket"`
	OpenTicket   string        `yaml:"openTicket"`
	Stages       []StageConfig `yaml:"stages"`
}

// Pipeline is the runtime representation of a pipeline built from config.
type Pipeline struct {
	CreateTicket string
	OpenTicket   string
	Stages       []StageConfig
	stageIndex   map[string]int // name -> index
}

// LoadPipeline loads and parses a pipeline configuration from a YAML file.
func LoadPipeline(path string) (*Pipeline, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read pipeline config: %w", err)
	}
	var cfg PipelineConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse pipeline config: %w", err)
	}
	if len(cfg.Stages) < 2 {
		return nil, fmt.Errorf("pipeline must have at least 2 stages")
	}
	p := &Pipeline{
		CreateTicket: cfg.CreateTicket,
		OpenTicket:   cfg.OpenTicket,
		Stages:       cfg.Stages,
		stageIndex:   make(map[string]int),
	}
	for i, s := range cfg.Stages {
		p.stageIndex[s.Name] = i
	}
	return p, nil
}

func (p *Pipeline) StageNames() []string {
	names := make([]string, len(p.Stages))
	for i, s := range p.Stages {
		names[i] = s.Name
	}
	return names
}

func (p *Pipeline) StageByName(name string) *StageConfig {
	if i, ok := p.stageIndex[name]; ok {
		return &p.Stages[i]
	}
	return nil
}

func (p *Pipeline) NextStage(name string) (string, bool) {
	i, ok := p.stageIndex[name]
	if !ok || i >= len(p.Stages)-1 {
		return "", false
	}
	return p.Stages[i+1].Name, true
}

func (p *Pipeline) RejectTarget(name string) (string, bool) {
	s := p.StageByName(name)
	if s == nil || !s.CanReject || s.RejectTo == "" {
		return "", false
	}
	if _, ok := p.stageIndex[s.RejectTo]; !ok {
		return "", false
	}
	return s.RejectTo, true
}

func (p *Pipeline) HasAgent(name string) bool {
	s := p.StageByName(name)
	return s != nil && s.Agent != nil
}

func (p *Pipeline) Title(name string) string {
	s := p.StageByName(name)
	if s != nil {
		return s.Title
	}
	return name
}

func (p *Pipeline) IsAutoSpawn(name string) bool {
	s := p.StageByName(name)
	return s != nil && s.Agent != nil && s.Auto
}
