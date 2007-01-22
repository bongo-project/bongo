/*------------------------------------------------------------------------------
* Copyright (C) 2003-2006 Ben van Klinken and the CLucene Team
* 
* Distributable under the terms of either the Apache License (Version 2.0) or 
* the GNU Lesser General Public License, as specified in the COPYING file.
------------------------------------------------------------------------------*/
#include "CLucene/StdHeader.h"
#include "TermVector.h"
#include "CLucene/util/StringBuffer.h"
#include "CLucene/util/Misc.h"

CL_NS_USE(util)
CL_NS_DEF(index)

 TermVectorsWriter::TermVectorsWriter(CL_NS(store)::Directory* directory, 
    const char* segment,FieldInfos* fieldInfos)
 {
    // Open files for TermVector storage
	char fbuf[CL_MAX_NAME];
	strcpy(fbuf,segment);
	char* fpbuf=fbuf+strlen(fbuf);

	strcpy(fpbuf,LUCENE_TVX_EXTENSION);
    tvx = directory->createOutput(fbuf);
    tvx->writeInt(FORMAT_VERSION);

	strcpy(fpbuf,LUCENE_TVD_EXTENSION);
    tvd = directory->createOutput(fbuf);
    tvd->writeInt(FORMAT_VERSION);
	
	strcpy(fpbuf,LUCENE_TVF_EXTENSION);
    tvf = directory->createOutput(fbuf);
    tvf->writeInt(FORMAT_VERSION);

    this->fieldInfos = fieldInfos;

	currentField = NULL;
	currentDocPointer = -1;
  }

  TermVectorsWriter::~TermVectorsWriter(){
      if ( tvx != NULL ){
          tvx->close();
        _CLDELETE(tvx);
      }
      if ( tvd != NULL ){
          tvd->close();
        _CLDELETE(tvd);
      }
      if ( tvf != NULL ){
          tvf->close();
        _CLDELETE(tvf);
      }
  }


  void TermVectorsWriter::openDocument() {
    closeDocument();

    currentDocPointer = tvd->getFilePointer();
  }


  void TermVectorsWriter::closeDocument(){
    if (isDocumentOpen()) {
      closeField();
      writeDoc();
      fields.clear();
      currentDocPointer = -1;
    }
  }


  bool TermVectorsWriter::isDocumentOpen() {
    return currentDocPointer != -1;
  }


  void TermVectorsWriter::openField(const TCHAR* field) {
    if (!isDocumentOpen())
		_CLTHROWA(CL_ERR_InvalidState,"Cannot open field when no document is open.");

    closeField();
    currentField = _CLNEW TVField(fieldInfos->fieldNumber(field));
  }

  void TermVectorsWriter::closeField(){
    if (isFieldOpen()) {
      /* DEBUG */
      //System.out.println("closeField()");
      /* DEBUG */

      // save field and terms
      writeField();
      fields.push_back(currentField);
      terms.clear();
      currentField = NULL;
    }
  }

  bool TermVectorsWriter::isFieldOpen() {
    return currentField != NULL;
  }

  void TermVectorsWriter::addTerm(const TCHAR* termText, int32_t freq) {
    if (!isDocumentOpen()) _CLTHROWA(CL_ERR_InvalidState,"Cannot add terms when document is not open");
    if (!isFieldOpen()) _CLTHROWA(CL_ERR_InvalidState,"Cannot add terms when field is not open");

    addTermInternal(termText, freq);
  }

  void TermVectorsWriter::addTermInternal(const TCHAR* termText, int32_t freq) {
    currentField->length += freq;
    TVTerm* term = _CLNEW TVTerm();
    term->setTermText(termText);
    term->freq = freq;
    terms.push_back(term);
  }


  void TermVectorsWriter::addVectors(TermFreqVector** vectors) {
    if (!isDocumentOpen()) _CLTHROWA(CL_ERR_InvalidState,"Cannot add term vectors when document is not open");
    if (isFieldOpen()) _CLTHROWA(CL_ERR_InvalidState,"Cannot add term vectors when field is open");

	int32_t i = 0;
	while ( vectors[i] != NULL ){
      addTermFreqVector(vectors[i]);
    }
  }

  void TermVectorsWriter::addTermFreqVector(TermFreqVector* v){
    if (!isDocumentOpen()) _CLTHROWA(CL_ERR_InvalidState,"Cannot add term vector when document is not open");
    if (isFieldOpen()) _CLTHROWA(CL_ERR_InvalidState,"Cannot add term vector when field is open");
    addTermFreqVectorInternal(v);
  }

  void TermVectorsWriter::addTermFreqVectorInternal(TermFreqVector* v){
    openField(v->getField());
    const TCHAR** terms = v->getTerms();
    const int32_t* freqs = v->getTermFrequencies();
    int32_t size = v->size();
    for (int32_t i = 0; i < size; i++) {
      addTermInternal(terms[i], freqs[i]);
    }
	_CLDELETE_ARRAY(terms);
    closeField();
  }

 
  
  
  void TermVectorsWriter::close() {
    try {
      closeDocument();

      // make an effort to close all streams we can but remember and re-throw
      // the first exception encountered in this process
	#define _DOTVWCLOSE(x) if (x != NULL){ \
		try { \
		  x->close(); _CLDELETE(x) \
        } catch (CLuceneError& e) { \
          if ( e.number() != CL_ERR_IO ) throw e; \
		  if (ikeep==0)ikeep=e.number(); \
          if (keep[0]==0) strcpy(keep,e.what()); \
		} catch (...) { \
			if (keep[0]==0) strcpy(keep,"Unknown error while closing " #x); \
		} \
	  }
	}_CLFINALLY( \
    char keep[200]; \
	  int32_t ikeep=0;
	  keep[0]=0; \
	  _DOTVWCLOSE(tvx); \
	  _DOTVWCLOSE(tvd); \
	  _DOTVWCLOSE(tvf); \
		if (keep[0] != 0 ) { \
			_CLTHROWA(ikeep,keep); \
		}
	);
  }

  

  void TermVectorsWriter::writeField()  {
    // remember where this field is written
    currentField->tvfPointer = tvf->getFilePointer();
    //System.out.println("Field Pointer: " + currentField.tvfPointer);
    int32_t size;

    tvf->writeVInt(size = terms.size());
    tvf->writeVInt(currentField->length - size);
    const TCHAR* lastTermText = LUCENE_BLANK_STRING;
	int32_t lastTermTextLen = 0;

    // write term ids and positions
    for (int32_t i = 0; i < size; i++) {
      TVTerm* term = (TVTerm*)terms[i];
      //tvf->writeString(term->termText);
	  int32_t start = CL_NS(util)::Misc::stringDifference(lastTermText, lastTermTextLen, 
		  term->getTermText(),term->getTermTextLen());
      int32_t length = term->getTermTextLen() - start;
      tvf->writeVInt(start);			  // write shared prefix length
      tvf->writeVInt(length);			  // write delta length
      tvf->writeChars(term->getTermText(), start, length);  // write delta chars
      tvf->writeVInt(term->freq);

      lastTermText = term->getTermText();
	  lastTermTextLen = term->getTermTextLen();
    }
  }




  void TermVectorsWriter::writeDoc()  {
    if (isFieldOpen()) _CLTHROWA(CL_ERR_InvalidState,"Field is still open while writing document");
    //System.out.println("Writing doc pointer: " + currentDocPointer);
    // write document index record
    tvx->writeLong(currentDocPointer);

    // write document data record
    int32_t size = fields.size();

    // write the number of fields
    tvd->writeVInt(size);

    // write field numbers
	{ //msvc6 scope fix
		for (int32_t i = 0; i < size; i++) {
		  TVField* field = (TVField*) fields[i];
		  tvd->writeVInt(field->number);
		}
	}

    // write field pointers
    int64_t lastFieldPointer = 0;
    { //msvc6 scope fix
		for (int32_t i = 0; i < size; i++) {
		  TVField* field = (TVField*) fields[i];
		  tvd->writeVLong(field->tvfPointer - lastFieldPointer);

		  lastFieldPointer = field->tvfPointer;
		}
	}
    //System.out.println("After writing doc pointer: " + tvx.getFilePointer());
  }


  const TCHAR* TermVectorsWriter::TVTerm::getTermText(){
      return termText;
  }
    size_t TermVectorsWriter::TVTerm::getTermTextLen(){ 
		if (termTextLen==-1)
			termTextLen = _tcslen(termText);
		return termTextLen; 
	}
	void TermVectorsWriter::TVTerm::setTermText(const TCHAR* val){ 
        _CLDELETE_CARRAY(termText);
		termText = STRDUP_TtoT(val);
		termTextLen = -1;

	}
	TermVectorsWriter::TVTerm::TVTerm(): freq(0){ 
        termText=NULL;  
        termTextLen=-1;
    }
    TermVectorsWriter::TVTerm::~TVTerm(){ 
        _CLDELETE_CARRAY(termText)
    }

CL_NS_END
