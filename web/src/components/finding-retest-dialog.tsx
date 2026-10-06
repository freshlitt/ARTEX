"use client";

import * as React from "react";

import { RotateCcwIcon } from "lucide-react";
import { toast } from "sonner";

import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Field, FieldDescription, FieldGroup, FieldLabel } from "@/components/ui/field";
import { Spinner } from "@/components/ui/spinner";
import { Textarea } from "@/components/ui/textarea";
import { api } from "@/lib/api";
import type { FindingRetest } from "@/lib/types";

interface FindingRetestDialogProps {
  findingId: string;
  findingName?: string;
  onClose: () => void;
  onStarted?: (retest: FindingRetest) => void;
}

// 열려 있을 때만 마운트，종료 후 지침 지우기；목록 및 세부 정보는 커밋 잠금 및 오류 처리를 공유합니다.，시작 후 현재 페이지에 유지。
export function FindingRetestDialog({ findingId, findingName, onClose, onStarted }: FindingRetestDialogProps) {
  const notesId = React.useId();
  const [notes, setNotes] = React.useState("");
  const [submitting, setSubmitting] = React.useState(false);
  const submitLock = React.useRef(false);

  async function start() {
    if (submitLock.current) return;
    submitLock.current = true;
    setSubmitting(true);
    try {
      const result = await api.startFindingRetest(findingId, notes.trim());
      onStarted?.(result.retest);
      onClose();
      toast.success(result.created ? "재테스트가 시작되었습니다，클릭 가능「재시험 중」대화 보기" : "이 취약점은 재테스트 중입니다.，은 기존 대화를 볼 수 있습니다.");
    } catch (e) {
      toast.error(`재테스트를 시작하지 못했습니다.：${(e as Error).message}`);
    } finally {
      submitLock.current = false;
      setSubmitting(false);
    }
  }

  return (
    <Dialog open onOpenChange={(open) => !open && !submitLock.current && onClose()}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>취약점 재테스트 #{findingId}</DialogTitle>
          <DialogDescription className="break-words">
            {findingName ? <span className="mb-2 block">{findingName}</span> : null}
            재테스트 Agent
            원본 증거와 테스트 제약 조건을 읽습니다.，독립형 세션에서 대상 검증 수행。재테스트가 성공적으로 완료되고 수리가 확인된 후，취약점 상태가 자동으로 다음으로 변경됩니다.「고정됨」，다른 결론은 그대로 유지。
          </DialogDescription>
        </DialogHeader>
        <FieldGroup>
          <Field data-disabled={submitting}>
            <FieldLabel htmlFor={notesId}>추가 설명（선택사항）</FieldLabel>
            <Textarea
              id={notesId}
              value={notes}
              maxLength={4000}
              rows={4}
              disabled={submitting}
              onChange={(e) => setNotes(e.target.value)}
              placeholder="예를 들면：원래 테스트 계정을 사용하여 원래 인터페이스를 확인하십시오.；수리버전은 v2。"
            />
            <FieldDescription>보충 수리 버전 사용 가능、이번에는 테스트 조건이나 제한사항。</FieldDescription>
          </Field>
        </FieldGroup>
        <DialogFooter>
          <Button variant="outline" disabled={submitting} onClick={onClose}>
            취소
          </Button>
          <Button disabled={submitting} onClick={() => void start()}>
            {submitting ? <Spinner data-icon="inline-start" /> : <RotateCcwIcon data-icon="inline-start" />}
            {submitting ? "생성 중…" : "재검사 시작"}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
