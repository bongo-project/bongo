/*------------------------------------------------------------------------------
* Copyright (C) 2003-2006 Ben van Klinken and the CLucene Team
* 
* Distributable under the terms of either the Apache License (Version 2.0) or 
* the GNU Lesser General Public License, as specified in the COPYING file.
------------------------------------------------------------------------------*/
#include "CLucene/StdHeader.h"
#include "CLucene/util/Reader.h"
#include "Field.h"
#include "CLucene/util/Misc.h"
#include "CLucene/util/StringIntern.h"
CL_NS_USE(util)
CL_NS_DEF(document) 

	Field::Field(const TCHAR* Name, const TCHAR* String, bool store, bool index, bool token, const bool storeTermVector)
	{
	//Func - Constructor
	//Pre  - Name != NULL and contains the name of the field
	//       String != NULL and contains the value of the field
	//       store indicates if the field must be stored
	//       index indicates if the field must be indexed
	//       token indicates if the field must be tokenized
	//Post - The instance has been created

		CND_PRECONDITION(Name != NULL, "Name is NULL");
		CND_PRECONDITION(String != NULL,"String is NULL");
		CND_PRECONDITION(!(!index && storeTermVector),"cannot store a term vector for fields that are not indexed.");

		_name        = CLStringIntern::intern( Name CL_FILELINE);
		_stringValue = stringDuplicate( String );
		_readerValue = NULL;

		_isStored = store;
		_isIndexed = index;
		_isTokenized = token;
      this->storeTermVector = storeTermVector;
      boost=1.0f;

	}

	Field::Field(const TCHAR* Name, Reader* reader, const bool storeTermVector){
	//Func - Constructor
	//Pre  - Name != NULL and contains the name of the field
	//       reader != NULL and contains a Reader
	//Post - The instance has been created with:
	//       isStored    = false
	//       isIndexed   = true
	//       isTokenized = true     
	//       meaning that the field only is indexed and tokenized

		CND_PRECONDITION(Name != NULL, "Name is NULL");
		CND_PRECONDITION(reader != NULL, "reader is NULL");

		_name        = CLStringIntern::intern( Name  CL_FILELINE);
		_stringValue = NULL;
		_readerValue = reader;

		_isStored = false;
		_isIndexed = true;
		_isTokenized = true;
      this->storeTermVector = storeTermVector;
      boost=1.0f;
	}

	Field::Field(const TCHAR* Name, Reader* reader, bool store, bool index, bool token, const bool storeTermVector)
	{
	//Func - Constructor
	//Pre  - Name != NULL and contains the name of the field
	//       reader != NULL and contains a Reader
	//       store indicates if the field must be stored
	//       index indicates if the field must be indexed
	//       token indicates if the field must be tokenized
	//Post - The instance has been created

		CND_PRECONDITION(Name != NULL, "Name is NULL");
		CND_PRECONDITION(reader != NULL, "reader is NULL");
		CND_PRECONDITION(!(!index && storeTermVector),"cannot store a term vector for fields that are not indexed.");

		_name        = CLStringIntern::intern( Name  CL_FILELINE);
		_stringValue = NULL;
		_readerValue = reader;

		_isStored = store;
		_isIndexed = index;
		_isTokenized = token;
      this->storeTermVector = storeTermVector;
      boost=1.0f;
	}

	Field* Field::Keyword(const TCHAR* Name, const TCHAR* Value) {
		return _CLNEW Field(Name, Value, true, true, false,false);
	}

	Field* Field::UnIndexed(const TCHAR* Name, const TCHAR* Value) {
		return _CLNEW Field(Name, Value, true, false, false,false);
	}

	Field* Field::Text(const TCHAR* Name, const TCHAR* Value, const bool storeTermVector) {
		return _CLNEW Field(Name, Value, true, true, true,storeTermVector);
	}

	Field* Field::UnStored(const TCHAR* Name, const TCHAR* Value, const bool storeTermVector) {
		return _CLNEW Field(Name, Value, false, true, true,storeTermVector);
	}

	Field* Field::Text(const TCHAR* Name, Reader* Value, const bool storeTermVector) {
		return _CLNEW Field(Name, Value,storeTermVector);
	}

	Field::~Field(){
	//Func - Destructor
	//Pre  - true
	//Post - Instance has been destroyed

		CLStringIntern::unintern(_name);
		_CLDELETE_CARRAY(_stringValue);
		_CLDELETE(_readerValue);
	}


	/*===============FIELDS=======================*/
	const TCHAR* Field::name() 		{ return _name; } ///<returns reference
	TCHAR* Field::stringValue()		{ return _stringValue; } ///<returns reference
	Reader* Field::readerValue()	{ return _readerValue; }

	bool	Field::isStored() 	{ return _isStored; }
	bool 	Field::isIndexed() 	{ return _isIndexed; }
	bool 	Field::isTokenized() 	{ return _isTokenized; }
   bool  Field::isTermVectorStored() { return storeTermVector; }

   void Field::setBoost(float_t boost) {
    this->boost = boost;
  }

  float_t Field::getBoost() {
    return boost;
  }

	TCHAR* Field::toString() {
		if (_isStored && _isIndexed && !_isTokenized)
			return Misc::join(_T("Keyword<"), _name, _T(":"), (_stringValue==NULL?_T("Reader"):_stringValue), _T(">"));
		else if (_isStored && !_isIndexed && !_isTokenized)
			return Misc::join(_T("Unindexed<"), _name, _T(":"), (_stringValue==NULL?_T("Reader"):_stringValue), _T(">"));
		else if (_isStored && _isIndexed && _isTokenized )
			return Misc::join(_T("Text<"), _name, _T(":"), (_stringValue==NULL?_T("Reader"):_stringValue), _T(">"));
      else if (!_isStored && _isIndexed && _isTokenized)
         return Misc::join(_T("UnStored<"), _name, _T(">"));
		else
			return Misc::join(_T("Field Object ("), _name, _T(")") );
	}

CL_NS_END
