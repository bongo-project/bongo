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

/* This is an implementation of sendmail designed to eventually conform to LSB:
 * http://refspecs.freestandards.org/LSB_2.1.0/LSB-Core-generic/LSB-Core-generic/baselib-sendmail-1.html
 * Other platforms may or may not find it useful.
 */

#include <config.h>
#include <xpl.h>
#include <errno.h>
#include <bongoutil.h>
#include <nmap.h>
#include <nmlib.h>
#include <mdb.h>
#include <stdio.h>
#include <stdlib.h>
#include <unistd.h>
#include <msgapi.h>
#include <pwd.h>

#include "sendmail.h"

static char*
LookupUser(uid_t uid) 
{
    char *ret;
    char *buf;
    int buflen;
    struct passwd pwbuf;
    struct passwd *pw;

    buflen = sysconf(_SC_GETPW_R_SIZE_MAX);
    buf = MemMalloc(buflen + 1);
#if defined(__SVR4) && defined(__sun) && !defined(_POSIX_PTHREAD_SEMANTICS)
    pw = getpwuid_r(uid, &pwbuf, buf, buflen);
#else
    getpwuid_r(uid, &pwbuf, buf, buflen, &pw);
#endif
    
    if (!pw) {
        MemFree(buf);
        return NULL;
    }

    ret = MemStrdup(pw->pw_name);
    MemFree(buf);
    return ret;
}


int 
main (int argc, char *argv[])
{
    Connection *nmap;
    MDBHandle directory;
    ssize_t size = 0;
    char *line = NULL;
    char *response;
    char command[200];
    int i;
      
    if (! MemoryManagerOpen("BongoSendmail")) {
        FatalError(1, "unable to initialise memory manager.");
    }
    
    ConnStartup((15*60), TRUE);
    
    if (! MDBInit()) {
        FatalError(1, "couldn't access MDB.");
    }
    directory = (MDBHandle)MsgInit();
    if (! directory) {
        FatalError(1, "invalid directory credentials.");
    }
    NMAPInitialize(directory);
    CONN_TRACE_INIT((char *)MsgGetWorkDir(NULL), "sendmail");
    // CONN_TRACE_SET_FLAGS(CONN_TRACE_ALL); /* uncomment this line and pass '--enable-conntrace' to autogen to get the agent to trace all connections */

    /* TODO: we only allow localhost at the moment, because that's where we're
     * getting our credentials. In the future, being able to remove the MDB 
     * stuff and connect to an arbitrary NMAP server would be nice.
     */
    nmap = NMAPConnectQueue("127.0.0.1", NULL);
    if (nmap == NULL) {
        FatalError(1, "cannot connect to NMAP server.");
    }
    
    response = MemMalloc(sizeof(char) * 1000);
    if (! NMAPAuthenticate(nmap, response, 1000)) {
        FatalError(1, "cannot authenticate with NMAP.");
    }
    MemFree(response);
    
    /* we're now all setup, and can start doing things */
    ParseArgs(argc, argv);
    
    /* start sending the email */
    NMAPSimple(nmap, "QCREA");
    
    /* send the mail body */
    NMAPSendCommand(nmap, "QSTOR MESSAGE\r\n", 15);
    
    line = MemMalloc(sizeof(char) * 1000);
    while ((size = GetLine(line, sizeof(char) * 1000, stdin)) && (size != -1)) {
        if ((line[0] == '.') && (size == 3)) {
            if (BongoSendmailConfig.inputmode == UNTIL_DOT) {
                /* this means end of mail */
                break;
            } else {
                /* this is mail content we need to pad to not confuse NMAP */
                NMAPSendCommand(nmap, "..\r\n", 3);
            }
        } else {
            /* pass the contents through to NMAP; this is the mail */
            char *text = line;
            if (! strncmp(line, "From:", 5) && BongoSendmailConfig.from_header) {
                NMAPSendCommand(nmap, "From: ", 6);
                text = BongoSendmailConfig.from_header;
            } else if ((! strncmp(line, "To:", 3)) || (! strncmp(line, "Cc:", 3))) {
                if (BongoSendmailConfig.extract) {
                    AddAddressees(line);
                }
            } else if (! strncmp(line, "Bcc:", 4)) {
                if (BongoSendmailConfig.extract) {
                    AddAddressees(line);
                }
                /* TODO: should only remove Bcc: when To: or Cc: is present, RFC 2822 */
                text = NULL;
            }
            if (text) {
                NMAPSendCommand(nmap, text, strlen(text));
            }
        }
    }
    NMAPSimple(nmap, ".");
    MemFree(line);
        
    /* send the mail envelope */
    for (i = 0; i < BongoSendmailConfig.addressees; i++) {
        char *address = BongoSendmailConfig.deliver_to[i];
        
        sprintf(command, "QSTOR TO %s %s 2", address, address);
        NMAPSimple(nmap, command);
    }
    
    if (! BongoSendmailConfig.from_envelope) {
        char *host;
        char *username = getenv("USER");
        if (! username) {
            username = getenv("LOGNAME");
        }

        if (! username) {
            username = LookupUser(getuid());
        }
        host = MemMalloc(256);
        gethostname(host, 256);
        BongoSendmailConfig.from_envelope = MemMalloc(1000);
        snprintf(BongoSendmailConfig.from_envelope, 1000,"%s@%s", username, host);
        MemFree(host);
    }
    
    sprintf(command, "QSTOR FROM %s -", BongoSendmailConfig.from_envelope);
    NMAPSimple(nmap, command);
    NMAPSimple(nmap, "QRUN");
    
    NMAPQuit(nmap);
    CONN_TRACE_SHUTDOWN();
    MemoryManagerClose("BongoSendmail");
    
    return(0);
}

void
ParseArgs (int argc, char *argv[])
{
    int arg = 1;
    int i;
    
    BongoSendmailConfig.inputmode = UNTIL_DOT;
    BongoSendmailConfig.extract = FALSE;
    
    while ((arg < argc) && (argv[arg][0] == '-')) {
        switch(argv[arg][1]) {
            case 'b':
                switch (argv[arg][2]) {
                    case 'p':
                        FatalError(50, "listing mail queue jobs not supported.");
                        PrintUsage();
                        break;
                    case 's':
                        FatalError(50, "running in smtpd mode is not supported.");
                        PrintUsage();
                        break;
                    case 'm':
                        BongoSendmailConfig.inputmode = UNTIL_DOT;
                        break;
                    default:
                        XplConsolePrintf("Unknown argument: %s\n", argv[arg]);
                        break;
                }
                break;
            case 'i':
                BongoSendmailConfig.inputmode = UNTIL_EOF;
                break;
            case 't':
                /* TODO: we don't remove addresses passed in argv when in -t i
                 * mode, which we should for LSB compliance. Horrible 
                 * behaviour tho'.
                 */
                BongoSendmailConfig.extract = TRUE;
                break;
            case 'F':
                arg++;
                BongoSendmailConfig.from_header = argv[arg];
                break;
            case 'f':
            case 'r':  /* postfix sendmail accepts -r ... */
                arg++;
                BongoSendmailConfig.from_envelope = argv[arg];
                break;
            case 'e':
                /* ignore these arguments */
                break;
            case 'o':
                if(argv[arg][2] == 'i') {
                    BongoSendmailConfig.inputmode = UNTIL_EOF;
                } 
                /* ignore other -o arguments */
                break;
            default:
                XplConsolePrintf("Unknown argument: %s\n", argv[arg]);
                break;
        }
        arg++;
    }
    
    for (i = 0; i < (argc - arg); i++) {
        AddAddressee(argv[arg]);
    }
}

void
AddAddressees (char *addressees)
{
    /* This is supposed to extract individual addresses from a header. It has
     * to deal with lines such as (from RFC 2822):
     * To: Mary Smith <mary@x.test>, jdoe@example.org, Who? <one@y.test>
     * Cc: <boss@nil.test>, "Giant; \"Big\" Box" <sysservices@example.net>
     * In this simple scenario, it's just pulling out things which look like 
     * actual addresses.
     */
    int start, i;
    int has_at = 0;
    int addressees_length;
    const char *invalid_email_chars = "<>, \t\r\n";
    char *email;
    
    addressees_length = strlen(addressees);
    start = -1;
    
    for (i = 0; i < addressees_length; i++) {
        int is_invalid_mail_char = 0;
        unsigned int j;
        
        for (j = 0; j < strlen(invalid_email_chars); j++) {
            if (addressees[i] == invalid_email_chars[j]) {
                is_invalid_mail_char = 1;   
            }
        }
        if (is_invalid_mail_char || (i == addressees_length)) {
            if ((start != -1) && has_at && ((i - start)<1000)) {
                email = MemMalloc ((sizeof(char) * (i - start)) + 1);
                strncpy (email, &addressees[start], i - start);
                email[i - start] = 0;
                AddAddressee(email);
            }
            start = -1;
            has_at = 0;
        } else if (addressees[i] == '@') {
            has_at = 1;
        } else {
            if (start == -1) start = i;        
        }
    }
}

void
AddAddressee (char *addressee)
{
    /* this is someone we want to send the mail to (ie., address envelope to) */
    int size;
    
    BongoSendmailConfig.addressees++;
    size = (sizeof(char *) * BongoSendmailConfig.addressees);
    if (BongoSendmailConfig.deliver_to) {
        BongoSendmailConfig.deliver_to = MemRealloc(BongoSendmailConfig.deliver_to, size);
    } else {
        BongoSendmailConfig.deliver_to = MemMalloc(size);
    }
    BongoSendmailConfig.deliver_to[BongoSendmailConfig.addressees - 1] = addressee;
}

void 
FatalError (const int exit_code, const char *message) 
{
    fprintf(stderr, "bongosendmail: %s\n", message);
    exit(exit_code);
}

void 
PrintUsage ()
{
    XplConsolePrintf("bongosendmail: fatal: usage: [options] [addresses]\n");
    exit(0);
}

int 
GetLine (char *buf, const unsigned int bufsize, FILE *fh)
{
    /* read a line from the file handle, make sure it's \r\n terminated */
    if (fgets (buf, bufsize - 1, fh ) == NULL) {
        return -1;
    } else {
        unsigned int strsize = strlen(buf);
        buf[strsize-1] = '\r';
        buf[strsize] = '\n';
        buf[strsize+1] = 0;
        return strsize + 1;
    }
}

void 
NMAPSimple (Connection *nmap, char *command)
{
    /* send NMAP a command and burp if it doesn't succeed */
    int repcode;
    char response[1024];
    
    NMAPSendCommand(nmap, command, strlen(command));
    NMAPSendCommand(nmap, "\r\n", 2);
    repcode = NMAPReadAnswer(nmap, response, sizeof(response), FALSE);
    if (repcode != 1000) {
        XplConsolePrintf("ERROR: Sent command %s\n", command);
        XplConsolePrintf("ERROR: NMAP server returned %d: %s\n", repcode, response);
        exit(-2);
    }
}
