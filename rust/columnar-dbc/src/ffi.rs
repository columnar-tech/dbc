// Copyright 2026 Columnar Technologies Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
use crate::{Error, Result};
use columnar_dbc_sys as sys;
use std::ffi::{c_char, CStr, CString};
use std::ptr;

pub(crate) fn cstring(s: impl Into<Vec<u8>>) -> Result<CString> {
    CString::new(s).map_err(|err| {
        Error::InvalidArgument(format!("{:?} contains a NUL byte", String::from_utf8_lossy(&err.into_vec())))
    })
}

/// A C input pointer for an optional string. The C API only reads inputs;
/// they are `char*` because cgo cannot express `const`.
pub(crate) fn ptr_or_null(s: &Option<CString>) -> *mut c_char {
    s.as_ref().map_or(ptr::null_mut(), |s| s.as_ptr().cast_mut())
}

/// Copies a string borrowed from a dbc object; NULL becomes `None`.
///
/// # Safety
/// `p` is NULL or a NUL-terminated string valid for the duration of the call.
unsafe fn opt_string(p: *const c_char) -> Option<String> {
    (!p.is_null()).then(|| CStr::from_ptr(p).to_string_lossy().into_owned())
}

/// Runs a C call that reports failure through a `dbc_error**` and converts
/// the error into [`Error::Dbc`].
pub(crate) fn check(call: impl FnOnce(*mut *mut sys::dbc_error) -> sys::dbc_status) -> Result<()> {
    let mut err: *mut sys::dbc_error = ptr::null_mut();
    if call(&mut err) == sys::DBC_OK {
        return Ok(());
    }
    if err.is_null() {
        return Err(Error::Dbc("dbc reported an error without details".into()));
    }
    // SAFETY: on DBC_ERROR the C API stored a live dbc_error that we own; the
    // message is copied before the error is freed.
    let message = unsafe { opt_string(sys::dbc_error_message(err)) };
    unsafe { sys::dbc_error_free(err) };
    Err(Error::Dbc(message.unwrap_or_default()))
}

/// The `dbc_list` an operation returned, freed on drop.
pub(crate) struct List(*mut sys::dbc_list);

impl List {
    /// Runs an operation that returns a `dbc_list` through `out`.
    pub(crate) fn from_call(
        call: impl FnOnce(*mut *mut sys::dbc_list, *mut *mut sys::dbc_error) -> sys::dbc_status,
    ) -> Result<Self> {
        let mut out = ptr::null_mut();
        check(|err| call(&mut out, err))?;
        Ok(Self(out))
    }

    pub(crate) fn len(&self) -> usize {
        // SAFETY: the list is live until drop.
        unsafe { sys::dbc_list_count(self.0) }
    }

    /// A row field; empty when it doesn't apply to this list.
    pub(crate) fn get(&self, row: usize, field: sys::dbc_field) -> String {
        // SAFETY: the list is live; the string is copied before it can be freed.
        unsafe { opt_string(sys::dbc_list_get(self.0, row, field)) }.unwrap_or_default()
    }

    /// A list-level field; `None` when absent.
    pub(crate) fn info(&self, field: sys::dbc_field) -> Option<String> {
        // SAFETY: as in `get`.
        unsafe { opt_string(sys::dbc_list_info(self.0, field)) }
    }
}

impl Drop for List {
    fn drop(&mut self) {
        // SAFETY: the list came from the C API and is freed exactly once.
        unsafe { sys::dbc_list_free(self.0) }
    }
}
