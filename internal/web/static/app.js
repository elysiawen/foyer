(function () {
  'use strict';

  /* ---------- Toast ---------- */

  var stack = document.getElementById('toast-stack');

  function dismiss(el) {
    el.classList.add('hide');
    setTimeout(function () {
      if (el.parentNode) {
        el.parentNode.removeChild(el);
      }
    }, 200);
  }

  function toast(message, kind) {
    if (!stack || !message) {
      return;
    }
    var isError = kind === 'error';

    var el = document.createElement('div');
    el.className = 'toast ' + (isError ? 'error' : 'success');

    var dot = document.createElement('span');
    dot.className = 'toast-dot';

    var text = document.createElement('span');
    text.className = 'toast-text';
    text.textContent = message;

    var close = document.createElement('button');
    close.type = 'button';
    close.className = 'toast-close';
    close.setAttribute('aria-label', '关闭');
    close.textContent = '×';
    close.addEventListener('click', function () {
      dismiss(el);
    });

    el.appendChild(dot);
    el.appendChild(text);
    el.appendChild(close);
    stack.appendChild(el);

    var timer = setTimeout(function () {
      dismiss(el);
    }, isError ? 6000 : 3200);
    el.addEventListener('mouseenter', function () {
      clearTimeout(timer);
    });
  }

  Array.prototype.forEach.call(document.querySelectorAll('.flash'), function (f) {
    toast(f.getAttribute('data-message'), f.getAttribute('data-kind'));
  });

  /* ---------- 弹窗 ---------- */

  function openModal(id) {
    var dlg = document.getElementById(id);
    if (dlg && typeof dlg.showModal === 'function' && !dlg.open) {
      dlg.showModal();
    }
  }

  document.addEventListener('click', function (e) {
    var opener = e.target.closest('[data-modal]');
    if (opener) {
      openModal(opener.getAttribute('data-modal'));
      return;
    }

    var closer = e.target.closest('[data-close]');
    if (closer) {
      var parent = closer.closest('dialog');
      if (parent) {
        parent.close();
      }
      return;
    }

    var danger = e.target.closest('[data-delete-url]');
    if (danger) {
      var form = document.getElementById('confirm-form');
      if (!form) {
        return;
      }
      form.action = danger.getAttribute('data-delete-url');
      document.getElementById('confirm-title').textContent =
        danger.getAttribute('data-delete-title') || '确认操作';
      document.getElementById('confirm-desc').textContent =
        danger.getAttribute('data-delete-desc') || '';
      document.getElementById('confirm-ok').textContent =
        danger.getAttribute('data-delete-ok') || '确定';
      openModal('confirm');
    }
  });

  // 点击遮罩关闭
  Array.prototype.forEach.call(document.querySelectorAll('dialog.modal'), function (dlg) {
    dlg.addEventListener('click', function (e) {
      if (e.target === dlg) {
        dlg.close();
      }
    });
  });

  // 服务端校验失败时自动把对应弹窗打开
  var autoOpen = document.body.getAttribute('data-auto-open');
  if (autoOpen) {
    openModal(autoOpen);
  }

  // 支持 /admin/apps?edit=3 这样的深链接
  var editID = new URLSearchParams(window.location.search).get('edit');
  if (editID) {
    openModal('app-edit-' + editID);
  }

  /* ---------- 应用搜索 ---------- */

  var input = document.getElementById('app-search');
  var grid = document.querySelector('[data-app-grid]');
  if (!input || !grid) {
    return;
  }

  var cards = Array.prototype.slice.call(grid.querySelectorAll('.app-card'));

  input.addEventListener('input', function () {
    var q = input.value.trim().toLowerCase();
    cards.forEach(function (card) {
      var name = (card.getAttribute('data-name') || '').toLowerCase();
      card.hidden = q !== '' && name.indexOf(q) === -1;
    });
  });
})();
