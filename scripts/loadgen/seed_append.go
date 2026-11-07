package main

import (
	"context"
	"fmt"
	"slices"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
)

// needsIMAP reports whether the plan puts any message outside INBOX, which
// only IMAP APPEND can fill.
func (s PlanSpec) needsIMAP() bool {
	return slices.ContainsFunc(s.Folders, func(f weighted[string]) bool { return f.Val != inboxFolder })
}

// appendFolders fills a user's non-INBOX folders with IMAP APPEND, after the
// INBOX mail is delivered. It reuses the msg-sending and msg-done records: a
// message's key (user, index) is unique across folders and non-INBOX messages
// never had a record, so a state file from before APPEND existed resumes by
// appending what it deferred.
//
// Like SMTP delivery it is at-least-once across a hard kill. A message left
// in the sending state is first looked up by Message-ID in its folder, which
// closes that window when the server supports header search.
//
// Errors name a user and message index and an error class, never server text.
func (r *seeder) appendFolders(ctx context.Context, up UserPlan) error {
	var todo []MessagePlan
	for _, mp := range up.Messages {
		if mp.Folder == inboxFolder {
			continue
		}
		if r.st.Msg(mp.User, mp.Index) == msgDone {
			r.counts.appendSkipped.Add(1)
			continue
		}
		todo = append(todo, mp)
	}
	if len(todo) == 0 || ctx.Err() != nil {
		return nil
	}
	fail := func(what string, err error) error {
		return fmt.Errorf("append user %d: %s: %s", up.Index, what, classNames[classifyIMAP(err, what == "login")])
	}
	sctx, cancel := stage(ctx)
	c, err := r.imap.connect(sctx)
	cancel()
	if err != nil {
		return fail("connect", err)
	}
	defer c.Close()
	// Every step finishes once started, like an SMTP send, so an interrupt
	// leaves no half-written message.
	step := func(fn func() error) error {
		sctx, cancel := stage(ctx)
		defer cancel()
		return imapBound(sctx, c, sendTimeout, fn)
	}
	pw := r.st.User(up.Index).pw
	if err := step(func() error { return c.Login(up.Email, pw).Wait() }); err != nil {
		return fail("login", err)
	}
	defer func() { _ = step(func() error { return c.Logout().Wait() }) }()

	var boxes []*imap.ListData
	if err := step(func() (e error) { boxes, e = c.List("", "*", nil).Collect(); return }); err != nil {
		return fail("list", err)
	}
	have := map[string]bool{}
	for _, b := range boxes {
		have[b.Mailbox] = true
	}
	for _, mp := range todo {
		if have[mp.Folder] {
			continue
		}
		// Create what LIST did not show rather than relying on an
		// ALREADYEXISTS code, which a server may not send.
		if err := step(func() error { return c.Create(mp.Folder, nil).Wait() }); err != nil {
			return fail("create", err)
		}
		have[mp.Folder] = true
	}
	for _, mp := range todo {
		if ctx.Err() != nil {
			return nil
		}
		if r.st.Msg(mp.User, mp.Index) == msgSending {
			if r.landed(step, c, mp) {
				if err := r.st.MsgDone(mp.User, mp.Index); err != nil {
					return err
				}
				r.counts.appendSkipped.Add(1)
				continue
			}
			r.counts.possiblyTwice.Add(1)
		}
		if err := r.st.Sending(mp.User, mp.Index); err != nil {
			return err
		}
		raw := r.pool.build(mp, up.Email)
		err := step(func() error {
			cmd := c.Append(mp.Folder, int64(len(raw)), nil)
			if _, err := cmd.Write(raw); err != nil {
				_ = cmd.Close()
				return err
			}
			if err := cmd.Close(); err != nil {
				return err
			}
			_, err := cmd.Wait()
			return err
		})
		if err != nil {
			return fail("append", err)
		}
		if err := r.st.MsgDone(mp.User, mp.Index); err != nil {
			return err
		}
		r.counts.appended.Add(1)
	}
	return nil
}

// landed reports whether the message is already in its folder, by Message-ID.
// Any doubt returns false: a repeat is the lesser harm than a gap.
func (r *seeder) landed(step func(func() error) error, c *imapclient.Client, mp MessagePlan) bool {
	var data *imap.SearchData
	err := step(func() error {
		if _, err := c.Select(mp.Folder, &imap.SelectOptions{ReadOnly: true}).Wait(); err != nil {
			return err
		}
		var e error
		data, e = c.UIDSearch(&imap.SearchCriteria{
			Header: []imap.SearchCriteriaHeaderField{{Key: "Message-ID", Value: mp.ID}},
		}, nil).Wait()
		return e
	})
	return err == nil && data != nil && len(data.AllUIDs()) > 0
}
