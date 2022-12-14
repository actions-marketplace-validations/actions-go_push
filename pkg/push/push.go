package push

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/actions-go/toolkit/core"
	"github.com/actions-go/toolkit/github"
	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/plumbing/protocol/packp/sideband"
	"github.com/go-git/go-git/v5/plumbing/transport/http"
)

func getInputOrDefault(name, dflt string) string {
	v, ok := core.GetInput(name)
	if !ok {
		return dflt
	}
	return v
}

func matchAny(path string, patterns []string) bool {
	for _, pattern := range patterns {
		if matched, err := filepath.Match(pattern, path); matched && err == nil {
			return true
		}
	}
	return false
}

func parseComaSeparatedPatterns(s string) []string {
	r := []string{}
	for _, p := range strings.Split(s, ",") {
		p = strings.TrimSpace(p)
		if p != "" {
			r = append(r, p)
		}
	}
	return r
}

func commit(repo *git.Repository, patterns, commitMessage, failIfEmpty string) error {
	paths := parseComaSeparatedPatterns(patterns)
	if len(paths) > 0 {
		core.Debugf("committing files matching patterns '%s'", paths)
	} else {
		core.Debugf("committing all updated tracked files")
	}
	wt, err := repo.Worktree()
	if err != nil {
		return err
	}
	status, err := wt.Status()
	if err != nil {
		return err
	}
	for path, s := range status {
		switch s.Worktree {
		case git.Untracked:
			if matchAny(path, paths) {
				core.Debugf("adding %s to index", path)
				err := gitadd(wt, path)
				if err != nil {
					return err
				}
			}
		case git.Unmodified, git.UpdatedButUnmerged:
		default:
			if len(paths) == 0 || matchAny(path, paths) {
				core.Debugf("adding %s to index", path)
				err := gitadd(wt, path)
				if err != nil {
					return err
				}
			}
		}
	}
	core.Debugf("getting worktree status")
	status, err = wt.Status()
	if err != nil {
		return err
	}
	empty := true
	for _, s := range status {
		switch s.Staging {
		case git.Unmodified, git.Untracked, git.UpdatedButUnmerged:
		default:
			empty = false
		}
	}
	if empty {
		core.Debug("no modified filed. Skipping commit")
		if failIfEmpty == "TRUE" {
			msg := fmt.Sprintf("nothing added to commit")
			core.SetFailed(msg)
			return fmt.Errorf(msg)
		}
	} else {
		core.Debugf("committing changes")
		err := gitcommit(wt, commitMessage, &git.CommitOptions{
			Author: &object.Signature{
				Name:  getInputOrDefault("author-name", "ActionsGo Bot"),
				Email: getInputOrDefault("author-email", "actions-go@users.noreply.github.com"),
				When:  time.Now(),
			},
		})
		if err != nil {
			return err
		}
	}
	return nil
}

func refName(name string) plumbing.ReferenceName {
	return plumbing.ReferenceName(name)
}

func refSpec(localRef, remoteRef, force string) string {
	refspec := fmt.Sprintf("%s:%s", localRef, remoteRef)
	if force == "TRUE" {
		refspec = "+" + refspec
	}
	return refspec
}

func push(repo *git.Repository, remoteName string, localRef, remoteRef plumbing.ReferenceName, failIfEmpty, force string) error {
	_, err := repo.Remote(remoteName)
	if err != nil {
		return err
	}
	if failIfEmpty == "FALSE" {
		localRef, err := repo.Reference(localRef, true)
		if err != nil {
			return err
		}
		remoteRef, err := repo.Reference(plumbing.NewRemoteReferenceName(remoteName, remoteRef.Short()), true)
		if err != nil {
			// remote exists but not its reference. going on with the push
		} else {
			if localRef.Hash().String() == remoteRef.Hash().String() {
				core.Debugf("No difference between local and remote ref. Skipping push")
				return nil
			}
		}
	}
	core.Debugf("pushing ref %s to %s/%s", localRef, remoteName, remoteRef)
	return repo.Push(&git.PushOptions{
		RemoteName: remoteName,
		RefSpecs:   []config.RefSpec{config.RefSpec(refSpec(localRef.String(), remoteRef.String(), force))},
		Progress:   sideband.Progress(os.Stdout),
		Auth: &http.BasicAuth{
			Username: "x-access-token", // anything except an empty string
			Password: getInputOrDefault("token", ""),
		},
	})
}

func Push(root string) error {
	repo, err := git.PlainOpen(root)
	if err != nil {
		return err
	}
	refName := getInputOrDefault("ref", "HEAD")
	ref, err := repo.Reference(plumbing.ReferenceName(refName), true)
	if err != nil {
		return err
	}
	branch, err := repo.Branch(ref.Name().Short())
	if err != nil {
		return err
	}
	remoteName := getInputOrDefault("remote", branch.Remote)
	remoteRefName := getInputOrDefault("remote-ref", branch.Merge.String())

	failIfEmpty := strings.ToUpper(getInputOrDefault("fail-if-empty", "FALSE"))
	force := strings.ToUpper(getInputOrDefault("force", "FALSE"))
	createCommit := strings.ToUpper(getInputOrDefault("create-commit", "TRUE"))
	commitMessage := getInputOrDefault("commit-message", fmt.Sprintf("[Auto] Update generated from github workflow %s/%s", github.Context.Workflow, github.Context.Action))

	if createCommit == "TRUE" {
		if err := commit(repo, getInputOrDefault("commit-files", ""), commitMessage, failIfEmpty); err != nil {
			return err
		}
	}
	return push(repo, remoteName, ref.Name(), plumbing.ReferenceName(remoteRefName), failIfEmpty, force)
}
