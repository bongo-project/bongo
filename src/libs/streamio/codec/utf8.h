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
UTF8_Decode(StreamStruct *Codec, StreamStruct *NextCodec)
{
	unsigned char	*Out=NextCodec->Start+NextCodec->Len;
	unsigned char	*OutEnd=NextCodec->End;
	unsigned long	Len=0;
	unsigned long	Sent;

	while (Len<Codec->Len) {
		/* We need to copy as much data as fits into the output buffer */
		if ((unsigned long)(OutEnd-Out)>(Codec->Len-Len)) {
			Sent=Codec->Len-Len;
		} else {
			Sent=OutEnd-Out;
		}
		memcpy(Out, Codec->Start+Len, Sent);
		Len+=Sent;
		NextCodec->Len+=Sent;
		Out+=Sent;

		FlushOutStream(6);
	}

	EndFlushStream;

	return(Len);
}


static int
UTF8_Encode(StreamStruct *Codec, StreamStruct *NextCodec)
{
	unsigned char	*Out=NextCodec->Start+NextCodec->Len;
	unsigned char	*OutEnd=NextCodec->End;
	unsigned long	Len=0;
	unsigned long	Sent;

	while (Len<Codec->Len) {
		/* We need to copy as much data as fits into the output buffer */
		if ((unsigned long)(OutEnd-Out)>(Codec->Len-Len)) {
			Sent=Codec->Len-Len;
		} else {
			Sent=OutEnd-Out;
		}
		memcpy(Out, Codec->Start+Len, Sent);
		Len+=Sent;
		NextCodec->Len+=Sent;
		Out+=Sent;

		FlushOutStream(6);
	}

	EndFlushStream;

	return(Len);
}
