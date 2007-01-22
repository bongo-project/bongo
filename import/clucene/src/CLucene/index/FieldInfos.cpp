/*------------------------------------------------------------------------------
* Copyright (C) 2003-2006 Ben van Klinken and the CLucene Team
* 
* Distributable under the terms of either the Apache License (Version 2.0) or 
* the GNU Lesser General Public License, as specified in the COPYING file.
------------------------------------------------------------------------------*/
#include "CLucene/StdHeader.h"
#include "FieldInfos.h"

#include "CLucene/store/Directory.h"
#include "CLucene/document/Document.h"
#include "CLucene/document/Field.h"
#include "CLucene/util/VoidMap.h"
#include "CLucene/util/Misc.h"
#include "FieldInfo.h"

CL_NS_USE(store)
CL_NS_USE(document)
CL_NS_DEF(index)


	FieldInfos::FieldInfos():byName(false,false),byNumber(true) {
	}

	FieldInfos::~FieldInfos(){
		byName.clear();
		byNumber.clear();
	}

	FieldInfos::FieldInfos(Directory* d, const char* name):
		byName(false,false),byNumber(true) 
	{
		IndexInput* input = d->openInput(name);
		try {	
			read(input);
		} _CLFINALLY (
		    input->close();
		    _CLDELETE(input);
		);
	}

	void FieldInfos::add(Document* doc) {
		DocumentFieldEnumeration* fields  = doc->fields();
		while (fields->hasMoreElements()) {
			Field* field = fields->nextElement();
			add(field->name(), field->isIndexed(), field->isTermVectorStored());
		}
		_CLDELETE(fields);
	}

	void FieldInfos::add( const TCHAR* name, const bool isIndexed, const bool storeTermVector) {
		FieldInfo* fi = fieldInfo(name);
      if (fi == NULL) {
         addInternal(name, isIndexed, storeTermVector); //todo: check this interning
      } else {
         if (fi->isIndexed != isIndexed) {
            fi->isIndexed = true;                      // once indexed, always index
         }
         if (fi->storeTermVector != storeTermVector) {
            fi->storeTermVector = true;                // once vector, always vector
         }
      }
	}

   void FieldInfos::add(const TCHAR** names,const bool isIndexed, const bool storeTermVectors) {
	  int32_t i=0;      
	  while ( names[i] != NULL ){
         add(names[i], isIndexed, storeTermVectors);
		 i++;
      }
   }

	int32_t FieldInfos::fieldNumber(const TCHAR* fieldName)const {
		FieldInfo* fi = fieldInfo(fieldName);
		if ( fi != NULL )
			return fi->number;
		else
			return -1;
	}


	FieldInfo* FieldInfos::fieldInfo(const TCHAR* fieldName) const {
		FieldInfo* ret = byName.get(fieldName);
		return ret;
	}
	const TCHAR* FieldInfos::fieldName(const int32_t fieldNumber)const {
		FieldInfo* fi = fieldInfo(fieldNumber);
        if ( fi == NULL )
            return LUCENE_BLANK_STRING;
		return fi->name;
	}

	FieldInfo* FieldInfos::fieldInfo(const int32_t fieldNumber) const {
		if ( fieldNumber < 0 || (size_t)fieldNumber >= byNumber.size() )
            return NULL;
        return byNumber[fieldNumber];
	}

	int32_t FieldInfos::size()const {
		return byNumber.size();
	}

	void FieldInfos::write(Directory* d, const char* name) {
		IndexOutput* output = d->createOutput(name);
		try {
			write(output);
		} _CLFINALLY (
		    output->close();
		    _CLDELETE(output);
		);
	}

	void FieldInfos::write(IndexOutput* output) {
		output->writeVInt(size());
		for (int32_t i = 0; i < size(); i++) {
			FieldInfo* fi = fieldInfo(i);
			uint8_t bits = 0x0;
			if (fi->isIndexed) bits |= 0x1;
			if (fi->storeTermVector) bits |= 0x2;

			output->writeString(fi->name,_tcslen(fi->name));
			output->writeByte(bits);
		}
	}

	void FieldInfos::read(IndexInput* input) {
		int32_t size = input->readVInt();
        TCHAR name[LUCENE_MAX_FIELD_LEN+1]; //todo: we make this very big, so that a jlucene field bigger than LUCENE_MAX_FIELD_LEN won't corrupt the buffer
		for (int32_t i = 0; i < size; i++){
			input->readString(name,LUCENE_MAX_FIELD_LEN);
			uint8_t bits = input->readByte();
			bool isIndexed = (bits & 0x1) != 0;
			bool storeTermVector = (bits & 0x2) != 0;

			addInternal(name, isIndexed, storeTermVector);
		}
	}
	void FieldInfos::addInternal( const TCHAR* name, const bool isIndexed, const bool storeTermVector) {
		FieldInfo* fi = _CLNEW FieldInfo(name, isIndexed, byNumber.size(), storeTermVector);
		byNumber.push_back(fi);
		byName.put( fi->name, fi);
	}

   bool FieldInfos::hasVectors() const{
    for (int32_t i = 0; i < size(); i++) {
       if (fieldInfo(i)->storeTermVector)
          return true;
    }
    return false;
  }
CL_NS_END
