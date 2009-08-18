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

#include <ical-wrapper.h>
#include <msgdate.h>
#include <memmgr.h>

extern BOOL MWHandleNamedTemplate(void *ClientIn, unsigned char *TemplateName, void *ObjectData);

static int
RFC2445_Decode(StreamStruct *Codec, StreamStruct *NextCodec)
{
	unsigned char	*In=Codec->Start;
	unsigned char	*Out=NextCodec->Start+NextCodec->Len;
	unsigned char	*OutEnd=NextCodec->End;
	unsigned long	Len=0;


	while (Len<Codec->Len) {
		FlushOutStream(6);

		if ((In[0]=='\r') || (In[0]=='\n')) {
			if ((Codec->Len-Len)<3) {
				if (Codec->EOS) {
					/* No more data, let's fake it */
					Out[0]=In[0];
					Out++;
					NextCodec->Len++;
					In++;
					Len++;
					continue;
				} else {
					/* Need more data? */
				    if (Len >= Codec->Min) {
					return(Len);
				    }

				    return(Codec->Len);
				}
			}
			if (In[1]==' ') {
				/* We're unfolding the line */
				In+=2;
				Len+=2;
				continue;
			} else if (In[1]=='\n' && In[2]==' ') {
				In+=3;
				Len+=3;
				continue;
			}
		}
		Out[0]=In[0];
		Out++;
		NextCodec->Len++;
		In++;
		Len++;
	}

	EndFlushStream;

	return(Len);
}

static int
RFC2445_Encode(StreamStruct *Codec, StreamStruct *NextCodec)
{
	unsigned char	*In=Codec->Start;
	unsigned char	*Out=NextCodec->Start+NextCodec->Len;
	unsigned char	*OutEnd=NextCodec->End;
	unsigned long	Len=0;


	while (Len<Codec->Len) {
		FlushOutStream(6);

		if (In[0]=='\n') {
			Codec->State=0;
		}
		Out[0]=In[0];
		Out++;
		In++;
		NextCodec->Len++;
		Len++;
		Codec->State++;
		if (Codec->State>74) {
			Out[0]='\r';
			Out[1]='\n';
			Out[2]=' ';
			Out+=3;
			NextCodec->Len+=3;
			Codec->State=0;
		}
	}

	EndFlushStream;

	return(Len);
}

/* There is no ML_Decode; it would not translate into something generically parsable */

static int
RFC2445_ML_Encode(StreamStruct *Codec, StreamStruct *NextCodec)
{
	unsigned char	*In=Codec->Start;
	unsigned char	*Out=NextCodec->Start+NextCodec->Len;
	unsigned char	*OutEnd=NextCodec->End;
	unsigned long	Len=0;

	while (Len<Codec->Len) {
		FlushOutStream(6);

		if (Codec->Len-Len<3) {
			if (!Codec->EOS) {
				/* Need more data */
			    if (Len >= Codec->Min) {
				return(Len);
			    }

			    return(Codec->Len);
			}
		}

		if (In[0]=='\r' && In[1]=='\n') {
			Out[0]='\\';
			Out[1]='n';
			In+=2;
			Len+=2;
			Out+=2;
			NextCodec->Len+=2;
			continue;
		} else if (In[0]=='\n') {
			Out[0]='\\';
			Out[1]='n';
			In++;
			Len++;
			Out+=2;
			NextCodec->Len+=2;
			continue;
		}
		Out[0]=In[0];
		Out++;
		In++;
		NextCodec->Len++;
		Len++;
	}

	EndFlushStream;

	return(Len);
}

static int
Calendar_Decode(StreamStruct *Codec, StreamStruct *NextCodec)
{
	unsigned char		*Out;
	ICalObject			*ICal;

	if (Codec->Len == 0) {
		return(Codec->Len);
	}

	/* We've got two parts; first we colled the data; once we get the EOS we'll parse and send it */
	Codec->StreamData=(void *)MemRealloc(Codec->StreamData, Codec->Len+Codec->StreamLength+1);
	if (Codec->StreamData) {
		memcpy((unsigned char *)Codec->StreamData+Codec->StreamLength, Codec->Start, Codec->Len);
		Codec->StreamLength+=Codec->Len;
	}

	if (!Codec->EOS) {
		return(Codec->Len);
	}

	/* Only get here with EOS; now process the ICal object */
	Out=(unsigned char *)(Codec->StreamData)+Codec->StreamLength;
	*Out='\0';

	ICal=ICalParseObject(NULL, Codec->StreamData, Codec->StreamLength);

	if (!ICal) {
		MemFree(Codec->StreamData);
		return(Codec->Len);	
	}

	/* Now call the template that handles displaying the ICal object */
	MWHandleNamedTemplate((void *)Codec->Client, "text/calendar", (void *)ICal);
	ICalFreeObjects(ICal);
	MemFree(Codec->StreamData);
	Codec->StreamData=NULL;

	return(Codec->Len);	
}
