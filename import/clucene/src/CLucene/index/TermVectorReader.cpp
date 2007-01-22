/*------------------------------------------------------------------------------
* Copyright (C) 2003-2006 Ben van Klinken and the CLucene Team
* 
* Distributable under the terms of either the Apache License (Version 2.0) or 
* the GNU Lesser General Public License, as specified in the COPYING file.
------------------------------------------------------------------------------*/
#include "CLucene/StdHeader.h"
#include "TermVector.h"
#include "CLucene/util/StringBuffer.h"

CL_NS_USE(util)
CL_NS_DEF(index)

TermVectorsReader::TermVectorsReader(CL_NS(store)::Directory* d, const char* segment, FieldInfos* fieldInfos){
	char fbuf[CL_MAX_NAME];
	strcpy(fbuf,segment);
	char* fpbuf=fbuf+strlen(fbuf);

	strcpy(fpbuf,LUCENE_TVX_EXTENSION);
	if (d->fileExists(fbuf)) {
      tvx = d->openInput(fbuf);
      checkValidFormat(tvx);
	  
	  strcpy(fpbuf,LUCENE_TVD_EXTENSION);
	  tvd = d->openInput(fbuf);
      checkValidFormat(tvd);
	  
	  strcpy(fpbuf,LUCENE_TVF_EXTENSION);
	  tvf = d->openInput(fbuf);
      checkValidFormat(tvf);

      _size = tvx->length() / 8;
	}else{
	  tvx = NULL;
	  tvd = NULL;
	  tvf = NULL;
	  _size = 0;
	}

    this->fieldInfos = fieldInfos;
}

TermVectorsReader::~TermVectorsReader(){
	close();
}

void TermVectorsReader::close(){
	SCOPED_LOCK_MUTEX(THIS_LOCK)

	// why don't we trap the exception and at least make sure that
    // all streams that we can close are closed?
	if (tvx != NULL){
		tvx->close();
	}
    if (tvd != NULL){
		tvd->close();
	}
    if (tvf != NULL){
		tvf->close();
	}
	_CLDELETE(tvx);
	_CLDELETE(tvd);
	_CLDELETE(tvf);
}

TermFreqVector* TermVectorsReader::get(const int32_t docNum, const TCHAR* field){
	SCOPED_LOCK_MUTEX(THIS_LOCK)
	
	// Check if no term vectors are available for this segment at all
    int32_t fieldNumber = fieldInfos->fieldNumber(field);
    TermFreqVector* result = NULL;
    if (tvx != NULL) {
      try {
        //We need to account for the FORMAT_SIZE at when seeking in the tvx
        //We don't need to do this in other seeks because we already have the file pointer
        //that was written in another file
		  tvx->seek((docNum * 8L) + TermVectorsWriter::FORMAT_SIZE);
        //System.out.println("TVX Pointer: " + tvx.getFilePointer());
        int64_t position = tvx->readLong();

        tvd->seek(position);
        int32_t fieldCount = tvd->readVInt();
        //System.out.println("Num Fields: " + fieldCount);
        // There are only a few fields per document. We opt for a full scan
        // rather then requiring that they be ordered. We need to read through
        // all of the fields anyway to get to the tvf pointers.
        int32_t number = 0;
        int32_t found = -1;
        for (int32_t i = 0; i < fieldCount; i++) {
          number += tvd->readVInt();
          if (number == fieldNumber) found = i;
        }
  
        // This field, although valid in the segment, was not found in this document
        if (found != -1) {
          // Compute position in the tvf file
          position = 0;
          for (int32_t i = 0; i <= found; i++)
          {
            position += tvd->readVLong();
          }
          result = readTermVector(field, position);
        }
        else {
          //System.out.println("Field not found");
        }
          
      } catch (CLuceneError& e) {
        printf("%s\n",e.what());
      }catch (...) { //todo: fix this
		  printf("Unknown error in TermVectorsReader::get\n");
      }
    }
	//todo: what should we do here?

    /*else
    {
      System.out.println("No tvx file");
    }*/
    return result;
}


TermFreqVector** TermVectorsReader::get(int32_t docNum){
	SCOPED_LOCK_MUTEX(THIS_LOCK)

	TermFreqVector** result = NULL;
    // Check if no term vectors are available for this segment at all
    if (tvx != NULL) {
      try {
        //We need to offset by
		tvx->seek((docNum * 8L) + TermVectorsWriter::FORMAT_SIZE);
        int64_t position = tvx->readLong();

        tvd->seek(position);
        int32_t fieldCount = tvd->readVInt();

        // No fields are vectorized for this document
        if (fieldCount != 0) {
            int32_t number = 0;
            const TCHAR** fields = _CL_NEWARRAY(const TCHAR*,fieldCount+1);
    		
			{ //msvc6 scope fix
				for (int32_t i = 0; i < fieldCount; i++) {
				    number += tvd->readVInt();
				    fields[i] = fieldInfos->fieldName(number);
				}
			}
			fields[fieldCount]=NULL;
		  
		    // Compute position in the tvf file
		    position = 0;
		    int64_t* tvfPointers = _CL_NEWARRAY(int64_t,fieldCount);
			{ //msvc6 scope fix
				for (int32_t i = 0; i < fieldCount; i++) {
				    position += tvd->readVLong();
				    tvfPointers[i] = position;
				}
			}

            result = (TermFreqVector**)readTermVectors(fields, tvfPointers, fieldCount);
            _CLDELETE_ARRAY(tvfPointers);
            _CLDELETE_ARRAY(fields);
        }
      } catch (CLuceneError& e) {
        printf("%s\n",e.what());
	  } catch (...){
	//todo: this is not good
		printf("Unknown error in TermVectorRead::get\n");
	  }
    }
    else
    {
	//todo: this is not good
      printf("No tvx file\n");
    }
    return result;
}


void TermVectorsReader::checkValidFormat(CL_NS(store)::IndexInput* in){
	int32_t format = in->readInt();
	if (format > TermVectorsWriter::FORMAT_VERSION)
	{
		CL_NS(util)::StringBuffer err;
		err.append(_T("Incompatible format version: "));
		err.appendInt(format);
		err.append(_T(" expected "));
		err.appendInt(TermVectorsWriter::FORMAT_VERSION);
		err.append(_T(" or less"));
		_CLTHROWT(CL_ERR_Runtime,err.getBuffer());
	}
}

SegmentTermVector** TermVectorsReader::readTermVectors(const TCHAR** fields, const int64_t* tvfPointers, const int32_t len){
	SegmentTermVector** res = _CL_NEWARRAY(SegmentTermVector*,len+1);
    for (int32_t i = 0; i < len; i++) {
      res[i] = readTermVector(fields[i], tvfPointers[i]);
    }
	res[len]=NULL;
    return res;
}
SegmentTermVector* TermVectorsReader::readTermVector(const TCHAR* field, const int64_t tvfPointer){
	// Now read the data from specified position
    //We don't need to offset by the FORMAT here since the pointer already includes the offset
    tvf->seek(tvfPointer);

    int32_t numTerms = tvf->readVInt();
    //System.out.println("Num Terms: " + numTerms);
    // If no terms - return a constant empty termvector
    if (numTerms == 0) 
		return _CLNEW SegmentTermVector(field, NULL, NULL);

    int32_t length = numTerms + tvf->readVInt();

    const TCHAR** terms = _CL_NEWARRAY(const TCHAR*,numTerms+1);
    int32_t* termFreqs = _CL_NEWARRAY(int32_t,numTerms+1); //todo: can't use null terminated,
                           // BUT, no problem because SegmentTermVector uses terms to count size

    int32_t start = 0;
    int32_t deltaLength = 0;
    int32_t totalLength = 0;
	TCHAR* buffer = NULL;
	int32_t bufferLen=0;
    const TCHAR* previousString = NULL;
	int32_t previousStringLen = 0;

    for (int32_t i = 0; i < numTerms; i++) {
      start = tvf->readVInt();
      deltaLength = tvf->readVInt();
      totalLength = start + deltaLength;
      if (bufferLen < totalLength)
      {
        _CLDELETE_CARRAY(buffer);
        buffer = _CL_NEWARRAY(TCHAR,totalLength);
		bufferLen = totalLength;

        for (int32_t j = 0; j < previousStringLen; j++)  // copy contents
          buffer[j] = previousString[j];
      }
      tvf->readChars(buffer, start, deltaLength);

	  TCHAR* tmp = _CL_NEWARRAY(TCHAR,totalLength+1);
	  _tcsncpy(tmp,buffer,totalLength);
	  tmp[totalLength] = '\0';
      terms[i] = tmp;

      previousString = terms[i];
	  previousStringLen = totalLength;

      termFreqs[i] = tvf->readVInt();
    }
    _CLDELETE_CARRAY(buffer);
	terms[numTerms]=NULL;
	termFreqs[numTerms]=0; //todo: can't use null terminated result!!!
    SegmentTermVector* tv = _CLNEW SegmentTermVector(field, terms, termFreqs);
    return tv;
}

int64_t TermVectorsReader::size(){
    return _size;
}
 



TermVectorOffsetInfo** TermVectorOffsetInfo::_EMPTY_OFFSET_INFO=NULL;

TermVectorOffsetInfo** TermVectorOffsetInfo::EMPTY_OFFSET_INFO(){
	if ( _EMPTY_OFFSET_INFO == NULL ){
		_EMPTY_OFFSET_INFO = _CL_NEWARRAY(TermVectorOffsetInfo*,1);
		_EMPTY_OFFSET_INFO[0] = NULL;
	}
	return _EMPTY_OFFSET_INFO;
}

TermVectorOffsetInfo::TermVectorOffsetInfo() {
	startOffset = 0;
	endOffset=0;
}
TermVectorOffsetInfo::~TermVectorOffsetInfo() {
}

TermVectorOffsetInfo::TermVectorOffsetInfo(int32_t startOffset, int32_t endOffset) {
	this->endOffset = endOffset;
	this->startOffset = startOffset;
}

int32_t TermVectorOffsetInfo::getEndOffset() {
	return endOffset;
}

void TermVectorOffsetInfo::setEndOffset(int32_t endOffset) {
	this->endOffset = endOffset;
}

int32_t TermVectorOffsetInfo::getStartOffset() {
	return startOffset;
}

void TermVectorOffsetInfo::setStartOffset(int32_t startOffset) {
	this->startOffset = startOffset;
}

bool TermVectorOffsetInfo::equals(TermVectorOffsetInfo* o) {
	if (this == o) 
		return true;

	//if (!(o instanceof TermVectorOffsetInfo)) return false;

	TermVectorOffsetInfo* termVectorOffsetInfo = o; //(TermVectorOffsetInfo) 

	if (endOffset != termVectorOffsetInfo->endOffset) return false;
	if (startOffset != termVectorOffsetInfo->startOffset) return false;

	return true;
}

size_t TermVectorOffsetInfo::hashCode() {
	size_t result;
	result = startOffset;
	result = 29 * result + endOffset;
	return result;
}
CL_NS_END
