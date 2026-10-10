// Copyright 2026 Dicer Authors
// SPDX-License-Identifier: MIT

//go:build linux

package agent

import (
	"fmt"
	"os/user"
	"strconv"
	"strings"
	"syscall"
)

// execUser is who a command runs as: the credential its process gets, and
// the home directory its HOME is set to.
type execUser struct {
	credential *syscall.Credential
	home       string
}

// execUserNamed returns who spec names, as user, uid, user:group or uid:gid,
// looked up in the guest's /etc/passwd and /etc/group. A user gets the
// groups /etc/group gives it. A uid or gid with no entry is taken as it is,
// and a uid with no entry runs with gid 0, no other groups and HOME /, as
// docker exec -u does. A name with no entry is an error.
func execUserNamed(spec string) (execUser, error) {
	name, group, hasGroup := strings.Cut(spec, ":")
	u := execUser{credential: &syscall.Credential{}, home: "/"}

	entry, err := userNamed(name)
	uid, isUID := idOf(name)
	switch {
	case err == nil:
		u.credential.Uid, _ = idOf(entry.Uid)
		u.credential.Gid, _ = idOf(entry.Gid)
		u.home = entry.HomeDir
		if gids, err := entry.GroupIds(); err == nil {
			for _, gid := range gids {
				if id, ok := idOf(gid); ok {
					u.credential.Groups = append(u.credential.Groups, id)
				}
			}
		}
	case isUID:
		u.credential.Uid = uid
	default:
		return execUser{}, fmt.Errorf("no user %q in the guest's /etc/passwd", name)
	}

	if hasGroup {
		gid, err := gidOf(group)
		if err != nil {
			return execUser{}, err
		}
		u.credential.Gid = gid
	}
	return u, nil
}

// userNamed returns the /etc/passwd entry of the user name, which may be a
// uid.
func userNamed(name string) (*user.User, error) {
	if _, ok := idOf(name); ok {
		return user.LookupId(name)
	}
	return user.Lookup(name)
}

// gidOf returns the gid of group, which may be a gid itself, looked up in
// /etc/group.
func gidOf(group string) (uint32, error) {
	if id, ok := idOf(group); ok {
		return id, nil
	}
	g, err := user.LookupGroup(group)
	if err != nil {
		return 0, fmt.Errorf("no group %q in the guest's /etc/group", group)
	}
	id, _ := idOf(g.Gid)
	return id, nil
}

// idOf returns s as a uid or gid, and whether it is one.
func idOf(s string) (uint32, bool) {
	id, err := strconv.ParseUint(s, 10, 32)
	return uint32(id), err == nil
}
