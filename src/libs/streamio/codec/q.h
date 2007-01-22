/****************************************************************************
 * <Novell-copyright>
 * Copyright (c) 2001, 2006 Novell, Inc. All Rights Reserved.
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


/* The "Q" encoding is defined in RFC 2047, and is very similar to
   "Quoted-Printable":

   (1) Any 8-bit value may be represented by a "=" followed by two
       hexadecimal digits.  For example, if the character set in use
       were ISO-8859-1, the "=" character would thus be encoded as
       "=3D", and a SPACE by "=20".  (Upper case should be used for
       hexadecimal digits "A" through "F".)

   (2) The 8-bit hexadecimal value 20 (e.g., ISO-8859-1 SPACE) may be
       represented as "_" (underscore, ASCII 95.).  (This character may
       not pass through some internetwork mail gateways, but its use
       will greatly enhance readability of "Q" encoded data with mail
       readers that do not support this encoding.)  Note that the "_"
       always represents hexadecimal 20, even if the SPACE character
       occupies a different code position in the character set in use.

   (3) 8-bit values which correspond to printable ASCII characters other
       than "=", "?", and "_" (underscore), MAY be represented as those
       characters.  (But see section 5 for restrictions.)  In
       particular, SPACE and TAB MUST NOT be represented as themselves
       within encoded words.
*/

static int
Q_Decode(StreamStruct *Codec, StreamStruct *NextCodec)
{
	unsigned char	*In=Codec->Start;
	unsigned char	*Out=NextCodec->Start+NextCodec->Len;
	unsigned char	*OutEnd=NextCodec->End;
	unsigned long	Len=0;


	while (Len<Codec->Len) {
		FlushOutStream(6);

		if (In[0]=='=') {
			if ((Codec->Len-Len)>=3) {
				In++;
				Len++;
				if ((In[0]=='\r') || (In[0]=='\n')) {
					/* According to spec, a '=\r\n' sequence is a soft newline and get's eaten; we also try to handle '=\n' only */
					In++;
					Len++;
					if (In[0]=='\n') {
						In++;
						Len++;
					}
				} else {
					/* It's a hex char to be decoded */
					In[0] |= 0x20;
					if (In[0] > 64) {
						Out[0]=(In[0]-'a'+10)<<4;
					} else {
						Out[0]=(In[0]-'0')<<4;
					}
					In++;
					Len++;

					In[0] |= 0x20;
					if (In[0] > 64) {
						Out[0] |= (In[0]-'a'+10);
					} else {
						Out[0] |= (In[0]-'0');
					}
					Out++;
					NextCodec->Len++;
					In++;
					Len++;
				}
			} else {
				if (Codec->EOS) {
					/* No more data, let's fake it */
					Out[0]=In[0];
					Out++;
					NextCodec->Len++;
					In++;
					Len++;
				} else {
					/* Need more data? */
				    if (Len >= Codec->Min) {
					return(Len);
				    }

				    return(Codec->Len);
				}
			}
                } else if ('_' == In[0]) {
                    Out[0] = 0x20;
                    Out++;
                    NextCodec->Len++;
                    In++;
                    Len++;
		} else {
			Out[0]=In[0];
			Out++;
			NextCodec->Len++;
			In++;
			Len++;
		}
	}

	EndFlushStream;

	return(Len);
}

static	unsigned char *QHEX = {"0123456789ABCDEF"};
#define	MAX_CHARS_PER_LINE	75

/* FIXME: this code is copied straight from the "quoted-printable" encoder.
   It would be nice to support the readability features specified by Q, such
   as representing ASCII 0x20 as the underscore character.
*/

static int
Q_Encode(StreamStruct *Codec, StreamStruct *NextCodec)
{
	unsigned char	*In=Codec->Start;
	unsigned char	*Out=NextCodec->Start+NextCodec->Len;
	unsigned char	*OutEnd=NextCodec->End;
	unsigned long	Len=0;


	while (Len<Codec->Len) {
		FlushOutStream(6);

		if ((Len+1==Codec->Len) && !Codec->EOS) {
		    if (Len >= Codec->Min) {
			return(Len);
		    }

		    return(Codec->Len);
		}

		if (In[0]=='\r' || In[0]=='\n') {
			Codec->State=0;		/* Char/per/Line/count */
			Out[0]=In[0];
			Out++;
			NextCodec->Len++;
			In++;
			Len++;
			if (Len<Codec->Len) {
				if (In[0]=='\n') {
					Out[0]=In[0];
					Out++;
					NextCodec->Len++;
					In++;
					Len++;
				} else {
					/* Broken line; there was a \n but on \r first; we're nice and fix it */
					Out[-1]='\r';
					Out[0]='\n';
					Out++;
					NextCodec->Len++;
				}
			}
		} else if ((In[0]<0x20) || (In[0]>0x7f) || (In[0]=='=') || (!Codec->EOS && ((In[0]==' ') && (In[1]=='\r')))) {		/* The last expression will find <SPACE><CR> and will make create linebreaks */
			if ((Codec->State+=3)>MAX_CHARS_PER_LINE) {			/* Watch it, we're incrementing Codec->State here!  */
				Out[0]='=';
				Out[1]='\r';
				Out[2]='\n';
				Out+=3;
				NextCodec->Len+=3;
				Codec->State=3;
			}
			Out[0]='=';
			Out[1]=QHEX[In[0] >> 4];
			Out[2]=QHEX[In[0] & 0x0f];
			In++;
			Len++;
			Out+=3;
			NextCodec->Len+=3;
		} else {
			if ((++Codec->State)>MAX_CHARS_PER_LINE) {			/* Watch it, we're incrementing Codec->State here! */
				Out[0]='=';
				Out[1]='\r';
				Out[2]='\n';
				Out+=3;
				NextCodec->Len+=3;
				Codec->State=3;
			}
			Out[0]=In[0];
			In++;
			Len++;
			Out++;
			NextCodec->Len++;
		}
	}

	EndFlushStream;

	return(Len);
}


