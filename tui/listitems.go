// SPDX-License-Identifier: BUSL-1.1
// Copyright (c) 2026 xnet-admin-1
//
// Use of this source code is governed by the Business Source License
// included in the LICENSE file.

package tui

import "time"

// modelItem implements list.Item for model picker
type modelItem string

func (i modelItem) Title() string       { return string(i) }
func (i modelItem) Description() string { return "" }
func (i modelItem) FilterValue() string { return string(i) }

// sessionItem implements list.Item for session picker
type sessionItem struct {
	id    string
	title string
	ts    int64
}

func (i sessionItem) Title() string       { return i.title }
func (i sessionItem) Description() string { return time.UnixMilli(i.ts).Format("Jan 02 15:04") }
func (i sessionItem) FilterValue() string { return i.title }
