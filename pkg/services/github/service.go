package github

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/grafana/grafana/pkg/api/response"
	"github.com/grafana/grafana/pkg/api/routing"
	"github.com/grafana/grafana/pkg/apimachinery/identity"
	"github.com/grafana/grafana/pkg/components/simplejson"
	"github.com/grafana/grafana/pkg/infra/log"
	"github.com/grafana/grafana/pkg/services/accesscontrol"
	contextmodel "github.com/grafana/grafana/pkg/services/contexthandler/model"
	"github.com/grafana/grafana/pkg/services/dashboards"
	"github.com/grafana/grafana/pkg/services/featuremgmt"
	"github.com/grafana/grafana/pkg/services/folder"
	"github.com/grafana/grafana/pkg/services/guardian"
	"github.com/grafana/grafana/pkg/services/org"
	"github.com/grafana/grafana/pkg/setting"
	"github.com/grafana/grafana/pkg/util"
	"github.com/grafana/grafana/pkg/web"
	"golang.org/x/oauth2"

	"github.com/google/go-github/v66/github"
)

// isDashboardRegex returns true if the path ends in `-dashboard.json`.
var isDashboardRegex = regexp.MustCompile(`-dashboard\.json$`)

// TODO: move this logic / endpoint to the app platform code path when it's ready
type Service struct {
	logger           log.Logger
	cfg              *setting.Cfg
	routeRegister    routing.RouteRegister
	dashboardService dashboards.DashboardService
	folderService    folder.Service
	features         featuremgmt.FeatureToggles
}

// TODO: use app platform entity
type repository struct {
	name          string
	url           string
	token         string
	webhookSecret []byte
	// TODO: this could be built for the user.
	webhookURL string
	orgID      int64
}

func ProvideService(
	cfg *setting.Cfg,
	features featuremgmt.FeatureToggles,
	routeRegister routing.RouteRegister,
	dashboardService dashboards.DashboardService,
	folderService folder.Service,
	// TODO: Fix this hack, see https://github.com/grafana/grafana-enterprise/issues/2935
	guardianProvider *guardian.Provider,
) *Service {
	logger := log.New("github.service")
	s := Service{
		logger:           logger,
		cfg:              cfg,
		routeRegister:    routeRegister,
		dashboardService: dashboardService,
		folderService:    folderService,
		features:         features,
	}

	if !features.IsEnabledGlobally(featuremgmt.FlagProvisioningV2) {
		return &s
	}

	logger.Info("Start GitHub service")

	s.registerRoutes()

	go func() {
		if err := s.initRepo(); err != nil {
			s.logger.Error("Failed to ensure webhook exists", "error", err)
		}
	}()

	return &s
}

// initRepo initializes the repository
// this must by replaced by an app platform implementation when the resource is created.
func (s *Service) initRepo() error {
	repo := s.getRepo()
	ctx := context.Background()

	if err := s.syncRepo(ctx, repo); err != nil {
		return fmt.Errorf("failed to sync repository: %w", err)
	}

	if err := s.ensureWebhookExists(ctx, repo); err != nil {
		return fmt.Errorf("failed to ensure webhook exists: %w", err)
	}

	// Sync again to ensure we didn't miss any updates in the meantime
	if err := s.syncRepo(ctx, repo); err != nil {
		return fmt.Errorf("failed to sync repository: %w", err)
	}

	return nil
}

func (s *Service) IsDisabled() bool {
	return s.features.IsEnabledGlobally(featuremgmt.FlagProvisioningV2)
}

// getRepo returns the repository based on the configuration.
// this must be replaced by an app platform implementation.
func (s *Service) getRepo() *repository {
	return &repository{
		name:          s.cfg.ProvisioningV2.RepositoryName,
		url:           s.cfg.ProvisioningV2.RepositoryURL,
		webhookSecret: []byte(s.cfg.ProvisioningV2.RepositoryWebhookSecret),
		webhookURL:    s.cfg.ProvisioningV2.RepositoryWebhookURL,
		token:         s.cfg.ProvisioningV2.RepositoryToken,
		orgID:         s.cfg.ProvisioningV2.RepositoryOrgID,
	}
}

func (s *Service) registerRoutes() {
	s.routeRegister.Group("/api/github", func(api routing.RouteRegister) {
		api.Post("/webhook", routing.Wrap(s.handleWebhook))
		api.Post("/sync", routing.Wrap(s.handleSync))
		api.Post("/push", routing.Wrap(s.handlePush))
		api.Post("/preview", routing.Wrap(s.handlePreview))
	})
}

func (s *Service) handleSync(c *contextmodel.ReqContext) response.Response {
	repo := s.getRepo()
	ctx := c.Req.Context()

	if err := s.syncRepo(ctx, repo); err != nil {
		return response.Error(500, "Failed to sync repository", err)
	}

	return response.Success("Repository synced successfully")
}

type PreviewRequest struct {
	Path string `json:"path"`
	Ref  string `json:"ref"`
}

type PreviewResponse struct {
	Path string          `json:"path"`
	Ref  string          `json:"ref"`
	Data json.RawMessage `json:"data"`
}

func (s *Service) handlePreview(c *contextmodel.ReqContext) response.Response {
	var req PreviewRequest
	if err := web.Bind(c.Req, &req); err != nil {
		return response.Error(400, "Failed to parse request", err)
	}

	ctx := c.Req.Context()
	repo := s.getRepo()
	githubClient := githubClientForRepo(repo)

	owner, name, err := extractOwnerAndRepo(repo.url)
	if err != nil {
		return response.Error(400, "Failed to extract owner and repo", err)
	}

	content, _, _, err := githubClient.Repositories.GetContents(ctx, owner, name, req.Path, &github.RepositoryContentGetOptions{
		Ref: req.Ref,
	})
	if err != nil {
		return response.Error(500, "Failed to get file content", err)
	}

	data, err := content.GetContent()
	if err != nil {
		return response.Error(500, "Failed to get file content", err)
	}

	return response.JSON(200, &PreviewResponse{
		Path: req.Path,
		Ref:  req.Ref,
		Data: json.RawMessage(data),
	})
}

type PushRequest struct {
	Branch  string          `json:"branch"`
	Message string          `json:"message"`
	Path    string          `json:"path"`
	Data    json.RawMessage `json:"data"`
}

func (s *Service) handlePush(c *contextmodel.ReqContext) response.Response {
	var req PushRequest
	if err := web.Bind(c.Req, &req); err != nil {
		return response.Error(400, "Failed to parse request", err)
	}

	ctx := c.Req.Context()
	repo := s.getRepo()
	githubClient := githubClientForRepo(repo)

	owner, name, err := extractOwnerAndRepo(repo.url)
	if err != nil {
		return response.Error(400, "Failed to extract owner and repo", err)
	}

	branch := "main"
	if req.Branch != "" {
		// create branch if it does not exist
		_, resp, err := githubClient.Repositories.GetBranch(ctx, owner, name, req.Branch, 0)
		if err != nil && resp.StatusCode == 404 {
			baseRef, _, err := githubClient.Repositories.GetBranch(ctx, owner, name, branch, 0)
			if err != nil {
				return response.Error(500, "Failed to get base branch", err)
			}

			if _, _, err := githubClient.Git.CreateRef(ctx, owner, name, &github.Reference{
				Ref: github.String(fmt.Sprintf("refs/heads/%s", req.Branch)),
				Object: &github.GitObject{
					SHA: baseRef.Commit.SHA,
				},
			}); err != nil {
				return response.Error(500, "Failed to create branch", err)
			}
		} else {
			return response.Error(400, "Branch does not exist", nil)
		}

		branch = req.Branch
	}

	// commit data to path in repository for the main branch
	file, _, resp, err := githubClient.Repositories.GetContents(ctx, owner, name, req.Path, &github.RepositoryContentGetOptions{
		Ref: branch,
	})
	if err != nil && resp.StatusCode != 404 {
		return response.Error(500, "Failed to get file content", err)
	}

	if file == nil {
		options := &github.RepositoryContentFileOptions{
			Message: github.String(req.Message),
			Content: req.Data,
			Branch:  github.String(branch),
		}

		if _, _, err := githubClient.Repositories.CreateFile(ctx, owner, name, req.Path, options); err != nil {
			return response.Error(500, "Failed to create file", err)
		}
		s.logger.Info("File created", "path", req.Path, "branch", branch)
	} else {
		options := &github.RepositoryContentFileOptions{
			Message: github.String(req.Message),
			Content: req.Data,
			Branch:  github.String(branch),
			SHA:     file.SHA,
		}
		if _, _, err := githubClient.Repositories.UpdateFile(ctx, owner, name, req.Path, options); err != nil {
			return response.Error(500, "Failed to update file", err)
		}
		s.logger.Info("File updated", "path", req.Path, "branch", branch)
	}

	return response.Success("File updated successfully")
}

func (s *Service) handleWebhook(c *contextmodel.ReqContext) response.Response {
	// TODO: check if the repo we care about based on <uid> in webhook URL.
	// TODO: generate a webhook secret for each repository
	// TODO: where to store the secret
	// TODO: how to deal with renamed repositories?
	// TODO: how to do the same for renamed files?
	// TODO: support for folders
	repo := s.getRepo()

	payload, err := github.ValidatePayload(c.Req, repo.webhookSecret)
	if err != nil {
		s.logger.Error("Failed to validate payload", "error", err)
		return response.Error(400, "Failed to validate payload", err)
	}

	event, err := github.ParseWebHook(github.WebHookType(c.Req), payload)
	if err != nil {
		return response.Error(400, "Failed to parse webhook event", err)
	}

	// TODO: will org work with webhook and app platform?
	ctx := c.Req.Context()

	// TODO: how to process events in order?
	switch event := event.(type) {
	case *github.PushEvent:
		s.logger.Info("Push event received", "data", event)

		if event.Repo == nil {
			return response.Error(400, "Ref is missing", nil)
		}

		if event.GetRepo().GetURL() != repo.url {
			return response.Error(400, "Repository URL mismatch", nil)
		}

		if event.GetRef() != "refs/heads/main" && event.GetRef() != "refs/heads/master" {
			// Return status 200 as you cannot configure the hook notifications per branch
			return response.Success(fmt.Sprintf("Skipped as %s is the main/master branch", event.GetRef()))
		}

		folder, err := s.ensureFolderExists(ctx, repo.orgID, repo.name)
		if err != nil {
			return response.Error(500, "Failed to ensure Github Sync folder exists", err)
		}

		githubClient := githubClientForRepo(repo)
		repoOwner := event.Repo.Owner.GetName()
		beforeRef := event.GetBefore()
		repoName := event.Repo.GetName()

		// TODO: support folders
		for _, commit := range event.Commits {
			s.logger.Info("Commit", "message", commit.GetMessage(), "author", commit.GetAuthor().GetLogin(), "timestamp", commit.GetTimestamp())

			for _, file := range commit.Added {
				s.logger.Info("File added", "file", file)
				if !s.isDashboard(file) {
					s.logger.Info("New file is not a dashboard", "file", file)
					continue
				}

				fileContent, _, _, err := githubClient.Repositories.GetContents(ctx, repoOwner, repoName, file, &github.RepositoryContentGetOptions{
					Ref: commit.GetID(),
				})
				if err != nil {
					return response.Error(500, "Failed to get file content", err)
				}

				content, err := fileContent.GetContent()
				if err != nil {
					return response.Error(500, "Failed to get file content", err)
				}

				if _, err := s.upsertDashboard(ctx, content, folder, repo.orgID, false); err != nil {
					return response.Error(500, "Failed to upsert dashboard", err)
				}

				s.logger.Info("New dashboard added", "file", file, "folder", folder.UID, "content", content, "dashboard")
			}

			for _, file := range commit.Modified {
				s.logger.Info("File modified", "file", file)
				if !s.isDashboard(file) {
					s.logger.Info("Modified file is not a dashboard", "file", file, "folder", folder.UID)
					continue
				}

				fileContent, _, _, err := githubClient.Repositories.GetContents(ctx, repoOwner, repoName, file, &github.RepositoryContentGetOptions{
					Ref: commit.GetID(),
				})
				if err != nil {
					return response.Error(500, "Failed to get file content", err)
				}

				content, err := fileContent.GetContent()
				if err != nil {
					return response.Error(500, "Failed to get file content", err)
				}

				if _, err := s.upsertDashboard(ctx, content, folder, repo.orgID, true); err != nil {
					return response.Error(500, "Failed to upsert dashboard", err)
				}
				s.logger.Info("Dashboard modified", "file", file, "content", content)
			}

			for _, file := range commit.Removed {
				s.logger.Info("File removed", "file", file)
				if !s.isDashboard(file) {
					s.logger.Info("Deleted file is not a dashboard", "file", file, "folder", folder.UID)
					continue
				}
				// get file content from the previous commit
				fileContent, _, _, err := githubClient.Repositories.GetContents(ctx, repoOwner, repoName, file, &github.RepositoryContentGetOptions{
					Ref: beforeRef,
				})
				if err != nil {
					return response.Error(500, "Failed to get file content", err)
				}

				content, err := fileContent.GetContent()
				if err != nil {
					return response.Error(500, "Failed to get file content", err)
				}

				if err := s.ensureDashboardDoesNotExist(ctx, content, folder, repo.orgID); err != nil {
					return response.Error(500, "Failed to ensure dashboard does not exist", err)
				}

				s.logger.Info("Dashboard removed", "file", file)
			}

			beforeRef = commit.GetID()
		}

	default:
		return response.Error(400, "Unsupported event type", nil)
	}

	return response.Success("event successfully processed")
}

func (s *Service) ensureDashboardDoesNotExist(ctx context.Context, content string, folder *folder.Folder, orgID int64) error {
	json, err := simplejson.NewJson([]byte(content))
	if err != nil {
		return fmt.Errorf("failed to parse file content: %w", err)
	}

	dashboard := dashboards.NewDashboardFromJson(json)
	query := &dashboards.GetDashboardQuery{
		UID: dashboard.UID,
		// TODO: limitation by dashboard title
		Title:     &dashboard.Title,
		FolderID:  &folder.ID,
		FolderUID: &folder.UID,
		OrgID:     orgID,
	}

	existingDashboard, err := s.dashboardService.GetDashboard(ctx, query)
	if err != nil && !errors.Is(err, dashboards.ErrDashboardNotFound) {
		return fmt.Errorf("failed to get existing dashboard: %w", err)
	}

	if errors.Is(err, dashboards.ErrDashboardNotFound) {
		s.logger.Warn("Deleted dashboard not found", "content", content)
		return nil
	}

	if err := s.dashboardService.DeleteDashboard(ctx, existingDashboard.ID, orgID); err != nil {
		return fmt.Errorf("failed to delete dashboard: %w", err)
	}

	return nil
}

func (s *Service) upsertDashboard(ctx context.Context, content string, folder *folder.Folder, orgID int64, shouldExist bool) (*dashboards.Dashboard, error) {
	json, err := simplejson.NewJson([]byte(content))
	if err != nil {
		return nil, fmt.Errorf("failed to parse file content: %w", err)
	}

	dashboard := dashboards.NewDashboardFromJson(json)
	dashboard.FolderID = folder.ID
	dashboard.FolderUID = folder.UID
	dashboard.OrgID = orgID
	dashboard.Updated = time.Now()

	query := &dashboards.GetDashboardQuery{
		UID: dashboard.UID,
		// TODO: limitation by dashboard title
		Title:     &dashboard.Title,
		FolderID:  &folder.ID,
		FolderUID: &folder.UID,
		OrgID:     orgID,
	}

	existingDashboard, err := s.dashboardService.GetDashboard(ctx, query)
	if err != nil && !errors.Is(err, dashboards.ErrDashboardNotFound) {
		return nil, fmt.Errorf("failed to get existing dashboard: %w", err)
	}

	if existingDashboard == nil {
		if shouldExist {
			s.logger.Warn("Dashboard should already exist", "content", content)
		}
		dashboard.Created = time.Now()
		dashboard.Version = 0
	} else {
		if !shouldExist {
			s.logger.Warn("Dashboard should not exist", "content", content)
		}

		dashboard.UID = existingDashboard.UID
		dashboard.Version = existingDashboard.Version + 1
	}

	dto := &dashboards.SaveDashboardDTO{
		OrgID:     orgID,
		UpdatedAt: time.Now(),
		User:      githubSyncUser(orgID),
		Overwrite: true,
		Dashboard: dashboard,
	}

	dashboard, err = s.dashboardService.ImportDashboard(ctx, dto)
	if err != nil {
		return nil, fmt.Errorf("failed to import dashboard: %w", err)
	}

	return dashboard, nil
}

func (s *Service) ensureFolderExists(ctx context.Context, orgID int64, name string) (*folder.Folder, error) {
	getQuery := &folder.GetFolderQuery{
		Title:        &name,
		OrgID:        orgID,
		SignedInUser: githubSyncUser(orgID),
	}

	syncFolder, err := s.folderService.Get(ctx, getQuery)
	switch {
	case err == nil:
		return syncFolder, nil
	case !errors.Is(err, dashboards.ErrFolderNotFound):
		return nil, err
	}

	createQuery := &folder.CreateFolderCommand{
		OrgID:        orgID,
		UID:          util.GenerateShortUID(),
		Title:        name,
		SignedInUser: githubSyncUser(orgID),
	}

	f, err := s.folderService.Create(ctx, createQuery)
	if err != nil {
		return nil, err
	}

	return f, nil
}

func (s *Service) isDashboard(file string) bool {
	return isDashboardRegex.MatchString(file)
}

func (s *Service) ensureWebhookExists(ctx context.Context, repo *repository) error {
	githubClient := githubClientForRepo(repo)

	owner, name, err := extractOwnerAndRepo(repo.url)
	if err != nil {
		return fmt.Errorf("error extracting owner and repo from URL: %w", err)
	}

	// List existing webhooks for the repository
	hooks, _, err := githubClient.Repositories.ListHooks(ctx, owner, name, nil)
	if err != nil {
		return fmt.Errorf("error listing webhooks: %w", err)
	}

	// Check if a webhook with the given URL already exists
	for _, hook := range hooks {
		if *hook.Config.URL == repo.webhookURL {
			s.logger.Info("Webhook already exists with URL:", "url", repo.webhookURL)
			return nil // Webhook already exists, no need to create
		}
	}

	secret := string(repo.webhookSecret)
	contentType := "json"
	hookConfig := &github.HookConfig{
		// TODO: build hook URL for users
		URL:         &repo.webhookURL,
		ContentType: &contentType,
		Secret:      &secret,
	}

	hookName := "grafana github sync hook"
	if _, _, err = githubClient.Repositories.CreateHook(ctx, owner, name, &github.Hook{
		Name:   &hookName,
		Config: hookConfig,
		Events: []string{"push"},
		Active: github.Bool(true),
	}); err != nil {
		return fmt.Errorf("error creating webhook: %w", err)
	}

	s.logger.Info("Webhook created successfully", "url", repo.webhookURL)

	return nil
}

func (s *Service) syncRepo(ctx context.Context, repo *repository) error {
	client := githubClientForRepo(repo)

	owner, name, err := extractOwnerAndRepo(repo.url)
	if err != nil {
		return fmt.Errorf("error extracting owner and repo from URL: %w", err)
	}

	folder, err := s.ensureFolderExists(ctx, repo.orgID, repo.name)
	if err != nil {
		return fmt.Errorf("error ensuring folder exists: %w", err)
	}

	existingDashboards, err := s.dashboardService.SearchDashboards(ctx, &dashboards.FindPersistedDashboardsQuery{
		OrgId:        repo.orgID,
		FolderUIDs:   []string{folder.UID},
		SignedInUser: githubSyncUser(repo.orgID),
	})
	s.logger.Info("Existing dashboards", "dashboards", existingDashboards, "folder", folder.UID, "orgID", repo.orgID)

	if err != nil {
		return fmt.Errorf("error getting dashboards: %w", err)
	}

	createdOrUpdatedDashboards, err := s.walkSyncRepo(ctx, repo, client, folder, owner, name, "", "main")
	if err != nil {
		return fmt.Errorf("error walking repository: %w", err)
	}

	// delete all dashboards that are not in the repository
	uids := make(map[string]bool)
	for _, d := range createdOrUpdatedDashboards {
		uids[d.UID] = true
	}

	for _, d := range existingDashboards {
		if _, ok := uids[d.UID]; !ok {
			if err := s.dashboardService.DeleteDashboard(ctx, d.ID, repo.orgID); err != nil {
				return fmt.Errorf("error deleting dashboard: %w", err)
			}

			s.logger.Info("Deleted dashboard", "dashboard", d.UID)
		}
	}

	return nil
}

func (s *Service) walkSyncRepo(ctx context.Context, repo *repository, client *github.Client, folder *folder.Folder, owner, name, path, branch string) ([]*dashboards.Dashboard, error) {
	content, contents, _, err := client.Repositories.GetContents(ctx, owner, name, path, &github.RepositoryContentGetOptions{
		Ref: branch,
	})
	if err != nil {
		return nil, fmt.Errorf("error getting contents for path '%s': %w", path, err)
	}

	if content.GetType() == "file" {
		if !s.isDashboard(content.GetPath()) {
			return nil, nil
		}

		fileContent, _, _, err := client.Repositories.GetContents(ctx, owner, name, content.GetPath(), &github.RepositoryContentGetOptions{
			// TODO: branch could change during the sync
			Ref: branch,
		})
		if err != nil {
			return nil, fmt.Errorf("error getting file content: %w", err)
		}

		bytes, err := fileContent.GetContent()
		if err != nil {
			return nil, fmt.Errorf("error getting file content: %w", err)
		}

		// TODO: do something smarter to not update what's already there.
		dashboard, err := s.upsertDashboard(ctx, bytes, folder, repo.orgID, true)
		if err != nil {
			return nil, fmt.Errorf("error upserting dashboard: %w", err)
		}

		s.logger.Info("Dashboard added or updated", "file", content.GetPath(), "folder", folder.UID, "content", content, "dashboard")
		return []*dashboards.Dashboard{dashboard}, nil
	}

	s.logger.Info("Directory", "path", content.GetPath())
	var result []*dashboards.Dashboard
	for _, content := range contents {
		dashboards, err := s.walkSyncRepo(ctx, repo, client, folder, owner, name, content.GetPath(), branch)
		if err != nil {
			return nil, err
		}
		result = append(result, dashboards...)
	}

	return result, nil
}

func githubClientForRepo(repo *repository) *github.Client {
	tokenSrc := oauth2.StaticTokenSource(
		&oauth2.Token{AccessToken: repo.token},
	)
	tokenClient := oauth2.NewClient(context.Background(), tokenSrc)
	return github.NewClient(tokenClient)
}

func githubSyncUser(orgID int64) identity.Requester {
	// this user has 0 ID and therefore, organization wide quota will be applied
	return accesscontrol.BackgroundUser(
		// TODO: API response shows that it was created by anonymous user
		"github_sync",
		orgID,
		org.RoleAdmin,
		[]accesscontrol.Permission{
			{Action: dashboards.ActionFoldersRead, Scope: dashboards.ScopeFoldersAll},
			{Action: dashboards.ActionFoldersCreate, Scope: dashboards.ScopeFoldersAll},
			{Action: dashboards.ActionFoldersDelete, Scope: dashboards.ScopeFoldersAll},
			{Action: dashboards.ActionDashboardsCreate, Scope: dashboards.ScopeFoldersAll},
			{Action: dashboards.ActionDashboardsWrite, Scope: dashboards.ScopeFoldersAll},
			{Action: dashboards.ActionDashboardsDelete, Scope: dashboards.ScopeFoldersAll},
			{Action: dashboards.ActionDashboardsRead, Scope: dashboards.ScopeFoldersAll},
		},
	)
}

// extractOwnerAndRepo takes a GitHub repository URL and returns the owner and repo name.
func extractOwnerAndRepo(repoURL string) (string, string, error) {
	parsedURL, err := url.Parse(repoURL)
	if err != nil {
		return "", "", fmt.Errorf("invalid URL: %w", err)
	}

	// Split the path to get owner and repo
	parts := strings.Split(strings.Trim(parsedURL.Path, "/"), "/")
	if len(parts) < 2 {
		return "", "", fmt.Errorf("URL does not contain owner and repo")
	}

	owner := parts[0]
	repo := parts[1]
	return owner, repo, nil
}
