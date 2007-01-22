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

#ifndef _MSGFTRS_H
#define _MSGFTRS_H

/*
    Any particular feature has an ID and a state.
    The ID is a letter (A-Z; a-z) and a number.
    The state (a letter) defines the inheritance order
   and if a particular feature is enabled or disabled.

    Example: Feature ID: A5 State: B

    Definition of states:

    X: Feature 
*/

/*
    States 
*/

#define FEATURE_USER_FIRST              'O'        /* Object first */
#define FEATURE_PARENT_FIRST            'P'        /* Parent first */
#define FEATURE_USE_USER                'U'        /* use User     */
#define FEATURE_USE_PARENT              'I'        /* Inherit      */
#define FEATURE_AVAILABLE               '1'        /* 1 = On       */
#define FEATURE_NOT_AVAILABLE           '0'        /* 0 = Off      */


/* IDs; need to be updated for every new feature to be provided
    For agents where multiple features are available, a complete 
   row should be used.
*/

#define FEATURE_IMAP                    'A', 1
#define FEATURE_POP                     'A', 2
#define FEATURE_ADDRESSBOOK             'A', 3
#define FEATURE_PROXY                   'A', 4
#define FEATURE_FORWARD                 'A', 5
#define FEATURE_AUTOREPLY               'A', 6
#define FEATURE_RULES                   'A', 7
#define FEATURE_FINGER                  'A', 8
#define FEATURE_SMTP_SEND_COUNT_LIMIT   'A', 9
#define FEATURE_SMTP_SEND_SIZE_LIMIT    'A', 10
#define FEATURE_NMAP_QUOTA              'A', 11
#define FEATURE_NMAP_STORE              'A', 12
#define FEATURE_WEBMAIL                 'A', 13
#define FEATURE_MODWEB                  'A', 14
#define FEATURE_MWMAIL_ADDRBOOK_P       'A', 15
#define FEATURE_MWMAIL_ADDRBOOK_S       'A', 16
#define FEATURE_MWMAIL_ADDRBOOK_G       'A', 17
#define FEATURE_MODWEB_WAP              'A', 18
#define FEATURE_CALAGENT                'A', 19
#define FEATURE_CALENDAR                'A', 20
#define FEATURE_ANTIVIRUS               'A', 21
#define FEATURE_SHARED_FOLDERS          'A', 22

#define DEFAULT_FEATURE_ROW_A           "AUUUUUUUUUUUUUUUUUUUUUUUUUUUUUUUUUUUUUUUU"
#define DEFAULT_FEATURE_ROW_B           "BUUUUUUUUUUUUUUUUUUUUUUUUUUUUUUUUUUUUUUUU"
#define DEFAULT_FEATURE_ROW_C           "CUUUUUUUUUUUUUUUUUUUUUUUUUUUUUUUUUUUUUUUU"
#define DEFAULT_FEATURE_ROW_D           "DUUUUUUUUUUUUUUUUUUUUUUUUUUUUUUUUUUUUUUUU"
#define DEFAULT_FEATURE_ROW_E           "EUUUUUUUUUUUUUUUUUUUUUUUUUUUUUUUUUUUUUUUU"

#ifdef __cplusplus
extern "C" {
#endif

/* The MSG Feature API Description */
EXPORT BOOL MsgGetUserFeature(const unsigned char *UserDN, unsigned char FeatureRow, unsigned long FeatureCol, unsigned char *Attribute, MDBValueStruct *VReturn);
EXPORT BOOL MsgDomainExists(const unsigned char *Domain, unsigned char *DomainObjectDN);

#ifdef __cplusplus
}
#endif

#endif /* _MSGFTRS_H */

