/*
 * Copyright (C) 1999-2001 Free Software Foundation, Inc.
 * This file is part of the GNU LIBICONV Library.
 *
 * The GNU LIBICONV Library is free software; you can redistribute it
 * and/or modify it under the terms of the GNU Library General Public
 * License as published by the Free Software Foundation; either version 2
 * of the License, or (at your option) any later version.
 *
 * The GNU LIBICONV Library is distributed in the hope that it will be
 * useful, but WITHOUT ANY WARRANTY; without even the implied warranty of
 * MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the GNU
 * Library General Public License for more details.
 *
 * You should have received a copy of the GNU Library General Public
 * License along with the GNU LIBICONV Library; see the file COPYING.LIB.
 * If not, write to the Free Software Foundation, Inc., 59 Temple Place -
 * Suite 330, Boston, MA 02111-1307, USA.
 */

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

static int
UCS_2_Decode(StreamStruct *Codec, StreamStruct *NextCodec)
{
    unsigned char *In = Codec->Start;
    unsigned char *Out = NextCodec->Start + NextCodec->Len;
    unsigned char *OutEnd = NextCodec->End;
    unsigned long Len = 0;

    while ((Len + 1) < Codec->Len) {
        FlushOutStream(5);
        UTF8EncodeChar(*((unicode *)In), Out, Out, NextCodec->Len);
        Len += 2;
        In += 2;
    }

    if (Len == Codec->Len) {
        EndFlushStream;
        return(Len);
    }

    /* this should never happen, but if it does, eat the odd character */
    Len++;
    EndFlushStream;
    return(Len);
}

static int
UCS_2_Encode(StreamStruct *Codec, StreamStruct *NextCodec)
{
    unsigned char *In = Codec->Start;
    unsigned char *Out = NextCodec->Start+NextCodec->Len;
    unsigned char *OutEnd = NextCodec->End;
    unsigned long Len = 0;
    unicode ucs2char;

    while (Len < Codec->Len) {
        FlushOutStream(5);
        CheckUTFInStream;
        UTF8DecodeChar(In, ucs2char, Len, Codec->Len);
        *((unicode *)Out) = ucs2char;
        Out += 2;
        NextCodec->Len += 2;
    }

    EndFlushStream;

    return(Len);
}
