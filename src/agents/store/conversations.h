/****************************************************************************
 * <Novell-copyright>
 * Copyright (c) 2005 Novell, Inc. All Rights Reserved.
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

#ifndef CONVERSATIONS_H_
#define CONVERSATIONS_H_

XPL_BEGIN_C_LINKAGE

#include "mail.h"

/* Find or create a conversation and set the info's "conversation" 
 * variable */

int GetConversation(DStoreHandle *handle,
                    DStoreDocInfo *info,
                    DStoreDocInfo *conversationOut);

int UpdateConversationMetadata(DStoreHandle *handle,
                               DStoreDocInfo *conversation);

int ConversationUpdateWithNewMail(DStoreHandle *handle,
                                  DStoreDocInfo *conversation,
                                  DStoreDocInfo *mail);

void GetConversationSourceMask(const char *sourceName,
                               uint32_t *required);

XPL_END_C_LINKAGE
                          
#endif /*CONVERSATIONS_H_*/
