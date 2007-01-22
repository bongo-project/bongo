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

typedef struct _RFC822AddressStruct {
    unsigned char      *Personal;
    unsigned char      *Mailbox;
    unsigned char      *Host;
    unsigned char      *ADL;
    struct _RFC822AddressStruct *Next;
} RFC822AddressStruct;

unsigned char       *RFC822ParseDomain(unsigned char *Address, unsigned char **end);
RFC822AddressStruct *RFC822ParseAddress(RFC822AddressStruct **List, RFC822AddressStruct *Last, unsigned char **Address, unsigned char *DefaultHost, unsigned long Depth);
BOOL                RFC822ParseAddressList(RFC822AddressStruct **List, unsigned char *Address, unsigned char *DefaultHost);
void                RFC822FreeAddressList(RFC822AddressStruct *List);
