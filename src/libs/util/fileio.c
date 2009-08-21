/****************************************************************************
 * <Novell-copyright>
 * Copyright (c) 2006 Novell, Inc. All Rights Reserved.
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

#include <config.h>
#include <xpl.h>
#include <bongoutil.h>


/* modifies: <count> bytes are copied from <source> to <dest>.  If
             <count> is 0, then the entire remainder of the file will
             be copied.  The file pointers will be advanced as the
             copy proceeds.

   returns: 0 on success, -1 on failure
*/
int
BongoFileCopyBytes(FILE *source, FILE *dest, size_t count)
{
    char buffer[4096];
    size_t rcount;
    size_t wcount;
    size_t total = count;

    while (!count || total) {
        if (count && total < sizeof(buffer)) {
            rcount = total;
        } else {
            rcount = sizeof(buffer);
        }

        rcount = fread(buffer, sizeof(char), rcount, source);
        if (!rcount) {
            if (ferror(source)) {
                return -1;
            }
            break;
        }

        wcount = fwrite(buffer, sizeof(char), rcount, dest);
        if (wcount != rcount) {
            /* FIXME: handle short write (bug 175398) */
            return -1;
        }

        total -= rcount;
    }

    return 0;
}




/* modifies: Bytes from range <start> to <end> of <source> are appended
             to the end of the <dest>.  <source> and <dest> can be the
             same file.  

   returns: 0 on success, -1 on failure
*/
int
BongoFileAppendBytes(FILE *source, FILE *dest, size_t start, size_t end)
{
    size_t count;
    char buffer[1024];

    while (start < end) {
        if (fseek(source, start, SEEK_SET)) {
            return -1;
        }
        count = (sizeof(buffer) < end - start) ? sizeof(buffer)
                                               : end - start;
        count = fread(buffer, sizeof(char), count, source);
        if (!count || 
            ferror(source) ||
            ferror(dest) ||
            fseek(dest, 0, SEEK_END) ||
            count != fwrite(buffer, sizeof(char), count, dest)) 
        {
            /* FIXME: handle short write (bug 175398) */
            return -1;
        }
        start += count;
    }
    return 0;
}
