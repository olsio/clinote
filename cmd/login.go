/*
 * This program is free software; you can redistribute it and/or modify
 * it under the terms of the GNU General Public License as published by
 * the Free Software Foundation; either version 3 of the License, or
 * (at your option) any later version.
 *
 * This program is distributed in the hope that it will be useful,
 * but WITHOUT ANY WARRANTY; without even the implied warranty of
 * MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
 * GNU General Public License for more details.
 *
 * You should have received a copy of the GNU General Public License
 * along with this program; if not, see <http://www.gnu.org/licenses/>.
 *
 * Copyright (C) Joakim Kennedy, 2016
 */

package main

import (
	"fmt"

	"github.com/TcM1911/clinote/evernote"
	"github.com/spf13/cobra"
)

var loginCmd = &cobra.Command{
	Use:   "login",
	Short: "Login user.",
	Long: `
Login authorizes CLInote to the server using OAuth.`,
	Run: func(cmd *cobra.Command, args []string) {
		client := defaultClient()
		defer client.Close()
		err := evernote.Login(client)
		if err == nil {
			fmt.Println("Authentication successful!")
		} else {
			fmt.Println("Authentication failed:", err.Error())
		}
	},
}

func init() {
	userCmd.AddCommand(loginCmd)
}
