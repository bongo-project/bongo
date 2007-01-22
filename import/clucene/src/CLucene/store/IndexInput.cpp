/*------------------------------------------------------------------------------
* Copyright (C) 2003-2006 Ben van Klinken and the CLucene Team
* 
* Distributable under the terms of either the Apache License (Version 2.0) or 
* the GNU Lesser General Public License, as specified in the COPYING file.
------------------------------------------------------------------------------*/
#include "CLucene/StdHeader.h"
#include "IndexInput.h"
#include "CLucene/util/Arrays.h"

CL_NS_USE(util)
CL_NS_DEF(store)

	IndexInput::IndexInput()
	{
	}
	IndexInput::IndexInput(const IndexInput& clone)
	{
	}

  int32_t IndexInput::readInt() {
    uint8_t b1 = readByte();
    uint8_t b2 = readByte();
    uint8_t b3 = readByte();
    uint8_t b4 = readByte();
    return ((b1 & 0xFF) << 24) | ((b2 & 0xFF) << 16) | ((b3 & 0xFF) <<  8)
         | (b4 & 0xFF);
  }

  int32_t IndexInput::readVInt() {
    uint8_t b = readByte();
    int32_t i = b & 0x7F;
    for (int32_t shift = 7; (b & 0x80) != 0; shift += 7) {
      b = readByte();
      i |= (b & 0x7F) << shift;
    }
    return i;
  }

  int64_t IndexInput::readLong() {
    int32_t i1 = readInt();
    int32_t i2 = readInt();
    return (((int64_t)i1) << 32) | (i2 & 0xFFFFFFFFL);
  }

  int64_t IndexInput::readVLong() {
    uint8_t b = readByte();
    int64_t i = b & 0x7F;
    for (int32_t shift = 7; (b & 0x80) != 0; shift += 7) {
      b = readByte();
      i |= (b & 0x7FL) << shift;
    }
    return i;
  }

  int32_t IndexInput::readString(TCHAR* buffer, const int32_t maxLength){
    int32_t len = readVInt();
	int32_t ml=maxLength-1;
    if ( len >= ml ){
      readChars(buffer, 0, ml);
      buffer[ml] = 0;
      //we have to finish reading all the data for this string!
      if ( len-ml > 0 )
         seek(getFilePointer()+(len-ml));
      return ml;
    }else{
      readChars(buffer, 0, len);
      buffer[len] = 0;
      return len;
    }
  }

   TCHAR* IndexInput::readString(const bool unique){
    int32_t len = readVInt();
      
   if ( len == 0){
      if ( unique )
         return stringDuplicate(LUCENE_BLANK_STRING);
      else
         return LUCENE_BLANK_STRING;
   }

    TCHAR* ret = _CL_NEWARRAY(TCHAR,len+1);
    readChars(ret, 0, len);
    ret[len] = 0;

    return ret;
  }

  void IndexInput::readChars( TCHAR* buffer, const int32_t start, const int32_t len) {
    const int32_t end = start + len;
    for (int32_t i = start; i < end; i++) {
      TCHAR b = readByte();
      if ((b & 0x80) == 0) {
        b = (b & 0x7F);
      } else if ((b & 0xE0) != 0xE0) {
        b = (((b & 0x1F) << 6)
          | (readByte() & 0x3F));
      } else {
          uint8_t b2 = readByte();
          uint8_t b3 = readByte();
		  b = (((b & 0x0F) << 12)
              | ((b2 & 0x3F) << 6)
              | (b3 & 0x3F));
      }
      buffer[i] = (TCHAR)b;
	}
  }






	BufferedIndexInput::BufferedIndexInput(int32_t _bufferSize):
		bufferStart(0),
		bufferLength(0),
		bufferPosition(0),
		bufferSize(_bufferSize),
		buffer(NULL)
  {
  }

  BufferedIndexInput::BufferedIndexInput(const BufferedIndexInput& clone):
    bufferStart(clone.bufferStart),
    bufferLength(clone.bufferLength),
    bufferPosition(clone.bufferPosition),
	bufferSize(clone.bufferSize),
    buffer(NULL)
  {
    /* DSR: Does the fact that sometime clone.buffer is not NULL even when
    ** clone.bufferLength is zero indicate memory corruption/leakage?
    **   if ( clone.buffer != NULL) { */
    if (clone.bufferLength != 0 && clone.buffer != NULL) {
      buffer = _CL_NEWARRAY(uint8_t,bufferLength);
	  memcpy(buffer,clone.buffer,bufferLength * sizeof(uint8_t));
    }
  }

  void BufferedIndexInput::readBytes(uint8_t* b, const int32_t offset, const int32_t len){
    if (len < bufferSize) {
      for (int32_t i = 0; i < len; i++)		  // read byte-by-byte
        b[i + offset] = (uint8_t)readByte();
    } else {					  // read all-at-once
      int64_t start = getFilePointer();
      seekInternal(start);
      readInternal(b, offset, len);

      bufferStart = start + len;		  // adjust stream variables
      bufferPosition = 0;
      bufferLength = 0;				  // trigger refill() on read
    }
  }

  int64_t BufferedIndexInput::getFilePointer() {
    return bufferStart + bufferPosition;
  }

  void BufferedIndexInput::seek(const int64_t pos) {
    if ( pos < 0 )
      _CLTHROWA(CL_ERR_IO, "IO Argument Error. Value must be a positive value.");
    if (pos >= bufferStart && pos < (bufferStart + bufferLength))
      bufferPosition = (int32_t)(pos - bufferStart);  // seek within buffer
    else {
      bufferStart = pos;
      bufferPosition = 0;
      bufferLength = 0;				  // trigger refill() on read()
      seekInternal(pos);
    }
  }
  void BufferedIndexInput::close(){
      if ( buffer != NULL ) {
      _CLDELETE_ARRAY(buffer);
      }

    bufferLength = 0;
    bufferPosition = 0;
    bufferStart = 0;
  }


  BufferedIndexInput::~BufferedIndexInput(){
    close();
  }

  void BufferedIndexInput::refill() {
    int64_t start = bufferStart + bufferPosition;
    int64_t end = start + bufferSize;
    if (end > length())				  // don't read past EOF
      end = length();
    bufferLength = (int32_t)(end - start);
    if (bufferLength == 0)
      _CLTHROWA(CL_ERR_IO, "IndexInput read past EOF");

    if (buffer == NULL){
      buffer = _CL_NEWARRAY(uint8_t,bufferSize);		  // allocate buffer lazily
    }
    readInternal(buffer, 0, bufferLength);


    bufferStart = start;
    bufferPosition = 0;
  }

CL_NS_END

