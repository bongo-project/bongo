/****************************************************************************
 * <Novell-copyright>
 * Copyright (c) 2001 Novell, Inc. All Rights Reserved.
 * 
 * This program is free software; you can redistribute it and/or
 * modify it under the terms of version 2 of the GNU General Public License
 * as published by the Free Software Foundation.
 * 
 * This program is distributed in the hope that it will be useful,
 * but WITHOUT ANY WARRANTY; without even the implied warranty of
 * MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
 * GNU General Public License for more details.
 * 
 * You should have received a copy of the GNU General Public License
 * along with this program; if not, contact Novell, Inc.
 * 
 * To contact Novell about this file by physical or electronic mail, you 
 * may find current contact information at www.novell.com.
 * </Novell-copyright>
 ****************************************************************************/

#ifndef _BONGO_SENDMAIL
#define _BONGO_SENDMAIL

enum InputMode {
    UNTIL_DOT, UNTIL_EOF,
};

struct config {
    char *from_header;
    char *from_envelope;
    
    enum InputMode inputmode;
    int extract;
    
    int addressees;
    char **deliver_to;
} BongoSendmailConfig;

/* bongosendmail.c */
void FatalError(const int error_code, const char *string);
void PrintUsage(void);
void NMAPSimple(Connection *nmap, char *command);
void ParseArgs(int argc, char *argv[]);
int GetLine(char *buffer, const unsigned int bufsize, FILE *handle);
void AddAddressees (char *addressees);
void AddAddressee (char *addressee);

#endif /* _BONGO_SENDMAIL */
